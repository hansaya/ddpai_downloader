package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/caarlos0/env/v7"
	"github.com/cavaliergopher/grab/v3"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	log "github.com/sirupsen/logrus"
)

// ErrSkipRecent means the file recently failed (e.g. EOF - likely deleted on camera); skip and continue with others.
var ErrSkipRecent = errors.New("skip: recently failed, likely deleted on camera")

var (
	FileHistory   = map[string]time.Time{}
	Exiting       bool
	cameraTZ      *time.Location
	cfg           Config
	camera        DdpaiCamera
)

// failedDownloads caches URLs that recently failed with EOF (file likely deleted on camera); skip for a while.
var (
	failedDownloads   = map[string]time.Time{}
	failedDownloadsMu sync.Mutex
	failedDownloadTTL = 15 * time.Minute
)

// Minimum size for valid video/photo files; smaller files are treated as corrupt (e.g. 58-byte stubs)
const minValidFileSize = 1024

type Config struct {
	HttpPort       string        `env:"HTTP_PORT" envDefault:"8080"`
	StoragePath    string        `env:"STORAGE_PATH" envDefault:"${PWD}" envExpand:"true"`
	CamURL         string        `env:"CAM_URL" envDefault:"http://193.168.0.1"`
	CameraTimeZone string        `env:"CAMERA_TIMEZONE" envDefault:"Local"`
	Interval       time.Duration `env:"INTERVAL" envDefault:"30s"`
	Timeout        time.Duration `env:"TIMEOUT" envDefault:"10s"`
	HistoryLimit   time.Duration `env:"RECORDING_HISTORY" envDefault:"96h"`
	LogLevel       string        `env:"LOG_LEVEL" envDefault:"info"`
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
	cameraTZ = time.Local
	cfg = Config{}
	if err := env.Parse(&cfg); err != nil {
		log.Error()
	} else {
		if cfg.CameraTimeZone != "" && !strings.EqualFold(cfg.CameraTimeZone, "Local") {
			if loc, err := time.LoadLocation(cfg.CameraTimeZone); err != nil {
				log.Warnf("invalid CAMERA_TIMEZONE %q, falling back to Local: %v", cfg.CameraTimeZone, err)
			} else {
				cameraTZ = loc
			}
		}
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
	cleanupStubs(cfg.StoragePath)
	updateTheFileHistory(cfg.StoragePath + "/recordings/")
	go checkDashCam(cfg.StoragePath, cfg.Interval, cfg.Timeout, cfg.HistoryLimit)

	e := echo.New()
	e.Use(middleware.LoggerWithConfig(middleware.LoggerConfig{
		Skipper: func(c echo.Context) bool {
			path := c.Path()
			return path == "/ping" || path == "/health"
		},
	}))
	e.Use(middleware.Recover())
	e.GET("/ping", func(c echo.Context) error {
		return c.JSON(http.StatusOK, struct{ Status string }{Status: "OK"})
	})
	e.GET("/health", healthHandler)
	e.Logger.Fatal(e.Start(":" + cfg.HttpPort))
}

// healthHandler returns 200 if storage is accessible, 503 otherwise.
// Does not check camera connectivity - the server is meant to wait for the camera.
func healthHandler(c echo.Context) error {
	recordingsPath := filepath.Join(cfg.StoragePath, "recordings")
	if err := os.MkdirAll(recordingsPath, 0755); err != nil {
		log.Warn("Health check failed: cannot access storage: ", err)
		return c.JSON(http.StatusServiceUnavailable, map[string]string{
			"status": "unhealthy",
			"reason": "storage inaccessible",
		})
	}
	return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
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
						if errors.Is(err, ErrSkipRecent) {
							continue
						}
						if err != nil {
							log.Warn(err)
							deleteFile(path)
							continue
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
						if errors.Is(err, ErrSkipRecent) {
							continue
						}
						if err != nil {
							log.Warn(err)
							deleteFile(path)
							continue
						}
						// Save the file name in the history
						FileHistory[path] = recording.date
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
						if errors.Is(err, ErrSkipRecent) {
							continue
						}
						if err != nil {
							log.Warn(err)
							deleteFile(path)
							continue
						}
						// Save the file name in the history
						FileHistory[path] = gpsFile.date
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
	p := filepath.FromSlash(path)
	log.WithFields(log.Fields{"file": p, "url": url})

	// If we already have a valid file, succeed regardless of failed cache (file exists = success)
	_, found := FileHistory[p]
	if found {
		log.Debug("File already downloaded ", p)
		return nil, p
	}
	if info, err := os.Stat(p); err == nil && info.Size() >= minValidFileSize {
		log.Debug("Skipping File ", p, " (valid, ", info.Size(), " bytes)")
		return nil, p
	}
	if info, err := os.Stat(p); err == nil && info.Size() < minValidFileSize {
		log.Warn("Removing corrupt stub (", info.Size(), " bytes) for retry: ", p)
		os.Remove(p)
	}

	// Skip files that recently failed with EOF (only when we don't already have the file)
	failedDownloadsMu.Lock()
	if t, ok := failedDownloads[url]; ok && time.Since(t) < failedDownloadTTL {
		failedDownloadsMu.Unlock()
		log.Debug("Skipping ", url, " (recently failed): ", p)
		return ErrSkipRecent, p
	}
	failedDownloadsMu.Unlock()

	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return err, p
	}

	const maxRetries = 3
	const retryDelay = 5 * time.Second
	var lastErr error
	for attempt := 1; attempt <= maxRetries; attempt++ {
		if attempt > 1 {
			log.Info("Retrying download (attempt ", attempt, "/", maxRetries, ") after ", retryDelay, ": ", url)
			time.Sleep(retryDelay)
		} else {
			log.Info("Downloading File ", url)
		}
		lastErr, p = doDownload(p, url, timeout, timestamp)
		if lastErr == nil {
			return nil, p
		}
		log.Warn("Download failed: ", lastErr)
	}
	// EOF/connection reset: only cache if we don't have a valid file (avoid false positives when file exists)
	if lastErr != nil && (strings.Contains(lastErr.Error(), "EOF") || strings.Contains(lastErr.Error(), "connection reset")) {
		if info, err := os.Stat(p); err != nil || info.Size() < minValidFileSize {
			failedDownloadsMu.Lock()
			failedDownloads[url] = time.Now()
			failedDownloadsMu.Unlock()
			log.Info("Marking as skipped for 15m: ", url)
		}
	}
	return lastErr, p
}

func doDownload(path string, url string, timeout time.Duration, timestamp time.Time) (err error, file string) {
	p := filepath.FromSlash(path)
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
				if errorCount == 2 {
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
			}
			if errorCount*2 > int(timeout.Seconds()) {
				resp.Cancel()
				return fmt.Errorf("Download Timeout"), p
			}
		case <-resp.Done:
			break Loop
		}
	}
	if err := resp.Err(); err != nil {
		// Camera may report EOF when transfer actually completed; if we got substantial data, treat as success
		if resp.Size() >= minValidFileSize {
			log.Info("Download completed with EOF (camera quirk); file valid: ", resp.Size(), " bytes")
			if e := os.Chtimes(resp.Filename, time.Now().Local(), timestamp); e != nil {
				log.Warn(e)
			}
			return nil, p
		}
		removePartialFile(resp.Filename)
		return err, p
	}
	// Validate file size; reject corrupt stubs (e.g. 58-byte placeholder)
	if resp.Size() < minValidFileSize {
		removePartialFile(resp.Filename)
		return fmt.Errorf("file too small (%d bytes), likely corrupt", resp.Size()), p
	}
	if err := os.Chtimes(resp.Filename, time.Now().Local(), timestamp); err != nil {
		log.Warn(err)
	}
	log.Info("Download completed ", resp.Duration(), " size:", resp.Size())
	return nil, p
}

func removePartialFile(path string) {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		log.Warn("Failed to remove partial file ", path, ": ", err)
	}
}

func deleteFile(path string) {
	log.Debug("Deleting file ", path)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		log.Warn("Failed to delete file ", path, ": ", err)
	}
}

func cleanupStubs(storagePath string) {
	for _, subdir := range []string{"recordings", "events"} {
		dir := filepath.Join(storagePath, subdir)
		files, err := ioutil.ReadDir(dir)
		if err != nil {
			if !os.IsNotExist(err) {
				log.Warn("Cannot read ", dir, ": ", err)
			}
			continue
		}
		for _, f := range files {
			if f.Size() < minValidFileSize {
				p := filepath.Join(dir, f.Name())
				log.Warn("Removing corrupt stub (", f.Size(), " bytes) on startup: ", p)
				os.Remove(p)
			}
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
		if file.Size() < minValidFileSize {
			continue // Stubs already cleaned by cleanupStubs; skip from history
		}
		date, err := camera.fileNameToDate(file.Name())
		if err != nil {
			log.Warn(err)
		} else {
			FileHistory[filepath.Join(p, file.Name())] = date
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
		if rec.Name == "" {
			log.Warn("Skipping recording entry with no filename, index: ", rec.Index)
			continue
		}
		if rec.Size <= 0 {
			log.Debug("Skipping recording still being written (size 0): ", rec.Name)
			continue
		}
		date, err := c.fileNameToDate(rec.Name)
		if err != nil {
			log.Warn("Skipping recording with unparseable filename: ", rec.Name, " error: ", err)
			continue
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
		// Skip malformed entries with no video file (e.g. corrupt/incomplete camera events)
		if event.Bvideoname == "" {
			log.Warn("Skipping malformed event entry with no video file, index: ", event.Index)
			continue
		}
		if event.Bvideosize == "" || event.Bvideosize == "0" {
			log.Debug("Skipping event still being written (no size): ", event.Bvideoname)
			continue
		}
		date, err := c.fileNameToDate(event.Bvideoname)
		if err != nil {
			// Skip entries where the filename can't be parsed instead of stopping everything
			log.Warn("Skipping event with unparseable filename: ", event.Bvideoname, " error: ", err)
			continue
		}
		list = append(list, File{
			name: event.Bvideoname,
			url:  c.camPath + "/" + event.Bvideoname,
			date: date,
		})
		// Only add thumbnail if it exists
		if event.Imgname != "" {
			list = append(list, File{
				name: event.Imgname,
				url:  c.camPath + "/" + event.Imgname,
				date: date,
			})
		}
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

	// Get GPS files
	for _, gpsF := range c.gpsFileList.File {
		if gpsF.Name == "" {
			log.Warn("Skipping GPS file entry with no filename, index: ", gpsF.Index)
			continue
		}
		date, err := c.fileNameToDate(gpsF.Name)
		if err != nil {
			log.Warn("Skipping GPS file with unparseable filename: ", gpsF.Name, " error: ", err)
			continue
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
	if fileName == "" {
		return time.Time{}, fmt.Errorf("invalid filename format: %q", fileName)
	}
	split := strings.Split(fileName, "_")
	var datePart string
	if len(split) == 4 {
		datePart = split[1]
	} else {
		datePart = split[0]
	}
	if datePart == "" || len(datePart) != 14 {
		return time.Time{}, fmt.Errorf("invalid filename format: %q", fileName)
	}
	date, err := time.ParseInLocation("20060102150405", datePart, cameraTZ)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid filename format: %q", fileName)
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
