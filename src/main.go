package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/caarlos0/env/v7"
	"github.com/cavaliergopher/grab/v3"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	log "github.com/sirupsen/logrus"
)

var (
	FileHistory   = map[string]time.Time{}
	Exiting       bool
	LocalTimeZone *time.Location
	cfg           Config
	camera        DdpaiCamera
)

type Config struct {
	HttpPort     string        `env:"HTTP_PORT" envDefault:"8080"`
	StoragePath  string        `env:"STORAGE_PATH" envDefault:"${PWD}" envExpand:"true"`
	CamURL       string        `env:"CAM_URL" envDefault:"http://193.168.0.1"`
	Interval     time.Duration `env:"INTERVAL" envDefault:"30s"`
	Timeout      time.Duration `env:"TIMEOUT" envDefault:"10s"`
	HistoryLimit time.Duration `env:"RECORDING_HISTORY" envDefault:"96h"`
	LogLevel     string        `env:"LOG_LEVEL" envDefault:"info"`
}

type EventList struct {
	Num   int `json:"num"`
	Event []struct {
		Index      string `json:"index"`
		Imgname    string `json:"imgname"`
		Bvideoname string `json:"bvideoname"`
		Bstarttime string `json:"bstarttime"`
		Bendtime   string `json:"bendtime"`
		Bvideosize string `json:"bvideosize"`
	} `json:"event"`
}

type PlaybackList struct {
	Num  int `json:"num"`
	File []struct {
		Index     string `json:"index"`
		Starttime string `json:"starttime"`
		Endtime   string `json:"endtime"`
		Name      string `json:"name"`
		Size      int    `json:"size,omitempty"`
	} `json:"file"`
}

type GpsFileList struct {
	Num  int `json:"num"`
	File []struct {
		Index      string `json:"index"`
		Type       string `json:"type"`
		Starttime  string `json:"starttime"`
		Endtime    string `json:"endtime"`
		Name       string `json:"name"`
		Parentfile string `json:"parentfile"`
	} `json:"file"`
}

type Session struct {
	AcSessionID string `json:"acSessionId"`
}

type JsonHeader struct {
	Errcode int    `json:"errcode"`
	Data    string `json:"data"`
}

type File struct {
	name string
	url  string
	date time.Time
}
type FileList []File

type DdpaiCamera struct {
	camPath      string
	session      Session
	eventList    EventList
	playbackList PlaybackList
	gpsFileList  GpsFileList
	httpClient   http.Client
}

func init() {
	t := time.Now()
	LocalTimeZone = t.Location()

	cfg = Config{}
	if err := env.Parse(&cfg); err != nil {
		log.Error()
	} else {
		// Set the default path to current dir
		if cfg.StoragePath == "" {
			currentDir, _ := os.Getwd()
			cfg.StoragePath = currentDir
		}
		// Set the log level
		switch strings.ToUpper(cfg.LogLevel) {
		case "ERROR":
			log.SetLevel(log.ErrorLevel)
		case "WARN":
			log.SetLevel(log.WarnLevel)
		case "DEBUG":
			log.SetLevel(log.DebugLevel)
		default:
			log.SetLevel(log.InfoLevel)
		}
	}
	// Setup our Ctrl+C handler
	SetupCloseHandler()
}

func main() {
	camera = makeCamera(cfg.CamURL, 1*time.Second)
	updateTheFileHistory(cfg.StoragePath + "/recordings/")
	go checkDashCam(cfg.StoragePath, cfg.Interval, cfg.Timeout, cfg.HistoryLimit)

	e := echo.New()
	e.Use(middleware.Logger())
	e.Use(middleware.Recover())
	e.GET("/ping", func(c echo.Context) error {
		return c.JSON(http.StatusOK, struct{ Status string }{Status: "OK"})
	})
	e.Logger.Fatal(e.Start(":" + cfg.HttpPort))
}

// SetupCloseHandler creates a 'listener' on a new goroutine which will notify the
// program if it receives an interrupt from the OS. We then handle this by calling
// our clean up procedure and exiting the program.
var quit = make(chan struct{})

func SetupCloseHandler() {
	c := make(chan os.Signal)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		log.Warn("Ctrl+C pressed in Terminal. Waiting 60S for downloads to complete..")
		Exiting = true
		close(quit)
		exitTimer := time.NewTimer(60 * time.Second)
		go func() {
			<-exitTimer.C
			log.Error("Failed to exit gracefully..")
			os.Exit(0)
		}()
	}()
}

func checkDashCam(mediaPath string, interval time.Duration, timeout time.Duration, historyLimit time.Duration) {
	ticker := time.NewTicker(interval)
	go func() {
		for {
			select {
			case <-ticker.C:
				// Exit when you can. This will help to prevent half written files
				if Exiting {
					os.Exit(0)
				}

				// Delete old videos
				count := checkHistory(historyLimit)
				if count > 0 {
					log.Info("Cleaned out ", count, " historic files...")
				}

				// Check whether camera can be reach before doing any requests
				if camera.connect() {
					// Get Event files
					log.Info("getting the event list...")
					err, eventList := camera.getEvents()
					if err != nil {
						log.Info("something went wrong with event list...")
						log.Warn(err)
						break
					}
					log.Info(len(eventList), " Event files found")
					for _, event := range eventList {
						err, path := downloadFile(mediaPath+"/events/"+event.name, event.url, timeout, event.date)
						if err != nil {
							log.Warn(err)
							deleteFile(path)
							break
						}
						// After done Downloading if asked, exit. This will help to prevent half written files
						if Exiting {
							os.Exit(0)
						}
					}

					// Get timelapse and continuous recordings
					err, recordingList := camera.getRecordings()
					if err != nil {
						log.Warn(err)
						break
					}
					log.Info(len(recordingList), " Recording files found")
					for _, recording := range recordingList {
						// Skip downloading old files
						if recording.date.Before(time.Now().Add(-historyLimit)) {
							log.Debug("Skipping .... Recording ", recording.name, " too old")
							continue
						}
						// Download
						err, path := downloadFile(mediaPath+"/recordings/"+recording.name, recording.url, timeout, recording.date)
						if err != nil {
							log.Warn(err)
							deleteFile(path)
							break
						} else {
							// Save the file name in the history
							FileHistory[path] = recording.date
						}
						// After done Downloading if asked, exit. This will help to prevent half written files
						if Exiting {
							os.Exit(0)
						}
					}

					// Get GPS files
					err, gpsList := camera.getGpsFiles()
					if err != nil {
						log.Warn(err)
						break
					}
					log.Info(len(gpsList), " GPS files found")
					for _, gpsFile := range gpsList {
						// Skip downloading old files
						if gpsFile.date.Before(time.Now().Add(-historyLimit)) {
							log.Debug("Skipping .... GPS ", gpsFile.name, " too old")
							continue
						}
						// Download
						err, path := downloadFile(mediaPath+"/recordings/"+gpsFile.name, gpsFile.url, timeout, gpsFile.date)
						if err != nil {
							log.Warn(err)
							deleteFile(path)
							break
						} else {
							// Save the file name in the history
							FileHistory[path] = gpsFile.date
						}
						// After done Downloading if asked, exit. This will help to prevent half written files
						if Exiting {
							os.Exit(0)
						}
					}
				} else {
					log.Warn("Cannot reach the Camera.. trying again in ", interval.String())
				}
			case <-quit:
				ticker.Stop()
				os.Exit(0)
				return
			}
		}
	}()
}

// Download media from the camera
func downloadFile(path string, url string, timeout time.Duration, timestamp time.Time) (err error, file string) {

	// Search the history first. Only works with continuous recordings
	p := filepath.FromSlash(path)
	log.WithFields(log.Fields{
		"file": p,
		"url":  url})
	_, found := FileHistory[p]
	if found {
		log.Debug("File already downloaded ", p)
		return nil, p
	}

	// Create the file
	if _, err := os.Stat(p); os.IsNotExist(err) {
		os.MkdirAll(filepath.Dir(p), 0700) // Create your file
	} else {
		// If we can find the file already, skip
		f, err := os.Open(p)
		if err == nil {
			defer f.Close()
			log.Debug("Skipping File ", p)
			return nil, p
		}
	}
	log.Info("Downloading File ", url)

	// Start downloading the file
	client := grab.NewClient()
	req, err := grab.NewRequest(filepath.Dir(p), url)
	if err != nil {
		return fmt.Errorf("Failed to connect"), p
	}
	req.NoResume = true // Not supported by the camera
	req.IgnoreRemoteTime = false
	resp := client.Do(req)
	errorCount := 1
	lastProgress := 0.0
	t := time.NewTicker(2000 * time.Millisecond)
	defer t.Stop()
Loop:
	for {
		select {
		case <-t.C:
			log.Debug("Progress ", fmt.Sprintf("%.2f", 100*resp.Progress()))

			if lastProgress == resp.Progress() {
				if errorCount == 2 { // Warn after 4 seconds
					log.Warn("Download not progressing. Timing out in ", timeout.Seconds())
				}
				errorCount++
			} else {
				errorCount = 0
				lastProgress = resp.Progress()
			}

			if Exiting {
				resp.Cancel()
				return fmt.Errorf("Downloading Stopped"), p
			} else if errorCount*2 > int(timeout.Seconds()) {
				resp.Cancel()
				return fmt.Errorf("Download Timeout"), p
			}

		case <-resp.Done:
			// download is complete
			break Loop
		}
	}

	// check for errors
	if err := resp.Err(); err != nil {
		return err, p
	}

	// Set the modified time
	currentTime := time.Now().Local()
	err = os.Chtimes(p, currentTime, timestamp)
	if err != nil {
		log.Warn(err)
	}

	log.Info("Download completed ", resp.Duration(), " size:", resp.Size())

	return nil, p
}

func deleteFile(path string) {
	log.Debug("Deleting file " + path)
	_, err := os.Open(path)
	if err == nil {
		e := os.Remove(path)
		if e != nil {
			log.Warn(e)
		}
	}
}

func updateTheFileHistory(path string) {
	p := filepath.FromSlash(path)
	files, err := ioutil.ReadDir(p)
	if err != nil {
		log.Warn(err)
		return
	}

	for _, file := range files {
		date, err := camera.fileNameToDate(file.Name())
		if err != nil {
			log.Warn(err)
		} else {
			FileHistory[p+file.Name()] = date
		}
	}
	log.Info("Found ", len(FileHistory), " saved items locally")
}

func checkHistory(length time.Duration) (count int) {
	for fileName, date := range FileHistory {
		if date.Before(time.Now().Add(-length)) {
			count++
			deleteFile(fileName)
			delete(FileHistory, fileName)
		}
	}
	return count
}

func makeCamera(camPath string, timeout time.Duration) DdpaiCamera {
	return DdpaiCamera{
		camPath:    camPath,
		httpClient: http.Client{Timeout: timeout},
	}
}

func (c *DdpaiCamera) connect() bool {
	_, err := c.httpClient.Get(c.camPath)
	if err != nil {
		c.reset()
		return false
	} else {
		if c.session.AcSessionID == "" {
			c.auth()
			c.requestCert()
		}
		return true
	}
}

func (c *DdpaiCamera) reset() {
	c.session.AcSessionID = ""
}

func (c *DdpaiCamera) getRecordings() (error, FileList) {
	var list FileList
	err := c.getJson(c.camPath+"/vcam/cmd.cgi?cmd=APP_PlaybackListReq", &c.playbackList)
	if err != nil {
		c.reset()
		return err, list
	}

	// Get timelapse and continuous recordings
	for i := range c.playbackList.File {
		rec := c.playbackList.File[len(c.playbackList.File)-i-1]
		date, err := c.fileNameToDate(rec.Name)
		if err != nil {
			return err, list
		}
		// Download
		list = append(list, File{
			name: rec.Name,
			url:  c.camPath + "/" + rec.Name,
			date: date,
		})
	}
	return nil, list
}

func (c *DdpaiCamera) getEvents() (error, FileList) {
	var list FileList
	err := c.getJson(c.camPath+"/vcam/cmd.cgi?cmd=APP_EventListReq", &c.eventList)
	if err != nil {
		c.reset()
		return err, list
	}

	// Get Event files
	for _, event := range c.eventList.Event {
		date, err := c.fileNameToDate(event.Bvideoname)
		if err != nil {
			return err, list
		}
		list = append(list, File{
			name: event.Bvideoname,
			url:  c.camPath + "/" + event.Bvideoname,
			date: date,
		})
		list = append(list, File{
			name: event.Imgname,
			url:  c.camPath + "/" + event.Imgname,
			date: date,
		})
	}
	return nil, list
}

func (c *DdpaiCamera) getGpsFiles() (error, FileList) {
	var list FileList
	err := c.getJson(c.camPath+"/vcam/cmd.cgi?cmd=API_GpsFileListReq", &c.gpsFileList)
	if err != nil {
		c.reset()
		return err, list
	}

	// Get Event files
	for _, gpsF := range c.gpsFileList.File {
		date, err := c.fileNameToDate(gpsF.Name)
		if err != nil {
			return err, list
		}
		list = append(list, File{
			name: gpsF.Name,
			url:  c.camPath + "/" + gpsF.Name,
			date: date,
		})
	}
	return nil, list
}

// Get the json output from the API call
func (c DdpaiCamera) getJson(url string, target interface{}) error {

	req, _ := http.NewRequest("GET", url, nil)
	if c.session.AcSessionID != "" {
		req.Header.Set("sessionid", c.session.AcSessionID)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Remove the header
	var jsonDump JsonHeader
	err = json.NewDecoder(resp.Body).Decode(&jsonDump)
	if err != nil {
		return err
	}

	return json.Unmarshal([]byte(jsonDump.Data), &target)
}

func (c *DdpaiCamera) auth() {
	c.getJson(c.camPath+"/vcam/cmd.cgi?cmd=API_RequestSessionID", &c.session)
}

func (c DdpaiCamera) fileNameToDate(fileName string) (stamp time.Time, err error) {
	split := strings.Split(fileName, "_")
	var date time.Time
	// Time laps vs normal video
	if len(split) == 4 {
		date, err = time.ParseInLocation("20060102150405", split[1], LocalTimeZone)
		if err != nil {
			return date, err
		}
	} else {
		date, err = time.ParseInLocation("20060102150405", split[0], LocalTimeZone)
		if err != nil {
			return date, err
		}
	}
	return date, nil
}

func (c DdpaiCamera) requestCert() error {

	var jsonData = []byte(`{
		"user": "admin",
		"password": "admin",
		"level": 0,
		"uid": "f2cf6a332999fbc3"}`)
	request, _ := http.NewRequest("POST", c.camPath+"/vcam/cmd.cgi?cmd=API_RequestCertificate", bytes.NewBuffer(jsonData))
	request.Header.Set("Cookie", "SessionID="+c.session.AcSessionID)
	request.Header.Set("sessionid", c.session.AcSessionID)
	request.Header.Set("Content-Type", "application/json")
	response, err := c.httpClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	return nil
}
