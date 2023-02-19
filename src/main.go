package main

import (
   "io/ioutil"
   "time"
   "fmt"
   "encoding/json"
   "net/http"
   "os"
   "path/filepath"
   "io"
   "os/signal"
   "syscall"
   "strings"
   // "strconv"
   // "context"
   log "github.com/sirupsen/logrus"
   "github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
   "github.com/caarlos0/env/v7"
)

var (
   //  log.Warnogger
   //  InfoLogger    *log.Logger
   //  ErrorLogger   *log.Logger
    FileHistory = map[string]time.Time{}
    Exiting bool
    LocalTimeZone *time.Location
    cfg config
//     t := time.Now()
//     zone, offset := t.Zone()
//     loc, _ := time.LoadLocation("Europe/Berlin")
)

type config struct {
	HttpPort     string        `env:"HTTP_PORT" envDefault:"8080"`
	StoragePath  string        `env:"STORAGE_PATH" envDefault:"${PWD}" envExpand:"true"`
	CamURL       string        `env:"CAM_URL" envDefault:"http://193.168.0.1"`
	Interval     time.Duration `env:"INTERVAL" envDefault:"30s"`
	Timeout      time.Duration `env:"TIMEOUT" envDefault:"60s"`
	HistoryLimit time.Duration `env:"RECORDING_HISTORY" envDefault:"96h"`
   LogLevel     string        `env:"LOG_LEVEL" envDefault:"info"`
}

type eventList struct {
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

type jsonHeader struct {
	Errcode int      `json:"errcode"`
	Data    string   `json:"data"`
}

func init() {
   t := time.Now()
   LocalTimeZone = t.Location()

   cfg = config{}
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
   updateTheFileHistory(cfg.StoragePath + "/continuos/")
   go checkDashCam(cfg.CamURL, cfg.StoragePath, cfg.Interval, cfg.Timeout, cfg.HistoryLimit)

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
      exitTimer := time.NewTimer(60*time.Second)
      go func() {
         <-exitTimer.C
         log.Error("Failed to exit gracefully..")
         os.Exit(0)
      }()
	}()
}

func checkDashCam(camPath string, mediaPath string, interval time.Duration, timeout time.Duration, historyLimit time.Duration) {
   ticker := time.NewTicker(interval)
   go func() {
      for {
         select {
         case <- ticker.C:
            // Exit when you can. This will help to prevent half written files
            if Exiting {
               os.Exit(0)
            }

            // Delete old videos
            count := checkHistory(historyLimit)
            if count > 0  {
               log.Info("Cleaned out ", count, " historic files...")
            }
            
            // Check whether camera can be reach before doing any requests
            var myClient = &http.Client{Timeout: 1 * time.Second}
            _, err := myClient.Get(camPath)

            if err != nil {
               log.Warn("Cannot reach the Camera.. trying again in ", interval.String())

            } else {
               var playbackList PlaybackList
               err := getJson(camPath + "/vcam/cmd.cgi?cmd=APP_PlaybackListReq", &playbackList)
               if err != nil {
                  log.Warn(err)
                  break
               }

               var EventList eventList
               err = getJson(camPath + "/vcam/cmd.cgi?cmd=APP_EventListReq", &EventList)
               if err != nil {
                  log.Warn(err)
                  break
               }

               // Get Event files
               log.Info(len(EventList.Event), " Event files found")
               for _, event := range EventList.Event {
                  err, path := downloadFile(mediaPath + "/events/" + event.Bvideoname, camPath + "/" + event.Bvideoname, timeout)
                  if err != nil  {
                     log.Println(err)
                     deleteFile(path)
                     break
                  }
                  // Download
                  err, path = downloadFile(mediaPath + "/events/" + event.Imgname, camPath + "/" + event.Imgname, timeout)
                  if err != nil  {
                     log.Println(err)
                     deleteFile(path)
                     break
                  }
                  // After done Downloading if asked, exit. This will help to prevent half written files
                  if Exiting {
                     os.Exit(0)
                  }
               }

               // Get timelapse and continuous recordings
               log.Info(len(playbackList.File), " Recording files found")
               for i := range playbackList.File {
                  file := playbackList.File[len(playbackList.File) - i - 1]
                  date, err := fileNameToDate(file.Name)
                  if err != nil {
                        log.Warn(err)
                        break
                  }
                  // Skip downloading old files
                  if date.Before(time.Now().Add(-historyLimit)) {
                     log.Debug("Skipping ", file.Index, ". Recording ", file.Name, " too old")
                     break
                  }
                  // Download
                  err, path := downloadFile(mediaPath + "/continuos/" + file.Name, camPath + "/" + file.Name, timeout)
                  if err != nil  {
                     log.Println(err)
                     deleteFile(path)
                     break
                  } else {
                     // Save the file name in the history
                     FileHistory[path] = date
                  }
                  // After done Downloading if asked, exit. This will help to prevent half written files
                  if Exiting {
                     os.Exit(0)
                  }
               }
            }
         case <- quit:
            ticker.Stop()
            os.Exit(0)
            return
         }
      }
   }()
}

// Get the json output from the API call
func getJson(url string, target interface{}) error {

   httpClient := &http.Client{Timeout: 4 * time.Second}
   resp, err := httpClient.Get(url)
   if err != nil {
      return err
   }
   defer resp.Body.Close()

   // Remove the header
   var test jsonHeader
   err = json.NewDecoder(resp.Body).Decode(&test)
   if err != nil {
      return err
   }

   return json.Unmarshal([]byte(test.Data), &target)
}

// Download media from the camera
func downloadFile(path string, url string, timeout time.Duration) (err error, file string) {

   p := filepath.FromSlash(path)
   // Search the history first. Only works with continuos recordings
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
   log.Info("Downloading File ", p)

   out, err := os.Create(p)
   if err != nil  {
      return err, p
   }
   defer out.Close()

   // Get the data
   var httpClient = &http.Client{Timeout: timeout}
   resp, err := httpClient.Get(url)
   if err != nil {
      return err, p
   }
   defer resp.Body.Close()

   // Check server response
   if resp.StatusCode != http.StatusOK {
      return fmt.Errorf("bad status: %s", resp.Status), p
   }

   // Writer the body to file
   _, err = io.Copy(out, resp.Body)
   if err != nil  {
      return err, p
   }

   return nil, p
}

func deleteFile (path string) {
   log.Debug("Deleting file " + path)
   e := os.Remove(path)
   if e != nil {
      log.Print(e)
   }
}

func updateTheFileHistory (path string) {
   p := filepath.FromSlash(path)
   files, err := ioutil.ReadDir(p)
   if err != nil {
      log.Fatal(err)
   }

   for _, file := range files {
      date, err := fileNameToDate(file.Name())
      if err != nil {
         log.Warn(err)
      } else {
         // fileInfo := FileInfo{file.Name(), date}
         FileHistory[p+file.Name()] = date
      }
      // log.Info(FileHistory[p+file.Name()])
   }
   log.Info("Found ", len(FileHistory), " saved items locally")
}

func fileNameToDate (fileName string) (stamp time.Time, err error) {
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

func checkHistory (length time.Duration) (count int) {
   for fileName, date := range FileHistory {
      if date.Before(time.Now().Add(-length)) {
         count++
         deleteFile(fileName)
         delete(FileHistory, fileName)
      }
   }
   return count
}