package main

import (
	"os"
	"bytes"
	"errors"
	"github.com/docker/docker/daemon/logger"
	"strings"
	"io/ioutil"
	"encoding/json"
)

var (
	errNoSuchDrivers = errors.New("no such drivers")
)

type subLogger struct {
	logger logger.Logger
	info   logger.Info
}

type teeLogger struct {
	loggers map[string]subLogger
}

type multipleError struct {
	message string
	errs    []error
}

type ReloadableLogger interface {
	Reload(*map[string]map[string]string) error
}

func newMultipleError(message string, errors []error) *multipleError {
	return &multipleError{message, errors}
}

func (e *multipleError) Error() string {
	buf := bytes.NewBufferString(e.message)
	for _, err := range e.errs {
		buf.WriteString("; " + err.Error())
	}
	return buf.String()
}

func (l *teeLogger) Reload(config *map[string]map[string]string) error {
	for name, l := range l.loggers {
		if driverConf, ok := (*config)[name]; ok {
			info := l.info
			configurationChanged := false
			for k, v := range driverConf {
				if v != info.Config[k] {
					info.Config[k] = v
					configurationChanged = true
				}
			}
			if configurationChanged {
				log.Infof("Configuration for %s changed, reloading logger %s", name, info.ContainerName)
				creator, err := logger.GetLogDriver(name)
				if err != nil {
					return err
				}
				newLogger, err := creator(info)
				if err != nil {
					return err
				}
				//switch to new logger
				l.logger = newLogger
			}
		}
	}
	return nil
}

func newTeeLogger(info logger.Info) (*teeLogger, error) {
	names, err := driverNames(info.Config)
	if err != nil {
		return nil, err
	}

	loggers := map[string]subLogger{}

	closeLoggers := func() {
		for _, l := range loggers {
			l.logger.Close()
		}
	}

	for _, name := range names {
		creator, err := logger.GetLogDriver(name)
		if err != nil {
			closeLoggers()
			return nil, err
		}

		newInfo := info
		newConfig, err := driverConfig(name, info.Config)
		if err != nil {
			log.WithError(err).Errorf("could not create logger %s", name)
			closeLoggers()
			return nil, err
		} else {
			newInfo.Config = newConfig
			log.Infof("adding logger %s with config %v", name, newInfo.Config)
		}

		l, err := creator(newInfo)
		if err != nil {
			log.WithError(err).Errorf("could not create logger %s", name)
			closeLoggers()
			return nil, err
		}
		loggers[name] = subLogger{l, newInfo}
	}

	return &teeLogger{loggers}, nil
}

func driverNames(config map[string]string) ([]string, error) {
	if s, ok := config["tee-drivers"]; ok {
		return strings.Split(s, ","), nil
	} else if s, ok := os.LookupEnv("TEE-DRIVERS"); ok {
		return strings.Split(s, ","), nil
	}
	return nil, errNoSuchDrivers
}

func driverConfig(driverName string, config map[string]string) (map[string]string, error) {
	newConfig := map[string]string{}
	for k, v := range config {
		ks := strings.SplitN(k, ":", 2)
		if len(ks) != 2 || ks[0] != driverName {
			continue
		}
		newConfig[ks[1]] = v
	}
	if fileExists("/etc/docker/tee.json") {
	  content, err := ioutil.ReadFile("/etc/docker/tee.json")
	  fileConfig := map[string]map[string]string{}
	  if err != nil {
        return nil, err
      }
	  if err := json.Unmarshal(content, &fileConfig); err != nil {
        return nil, err
	  }
	  if val, ok := fileConfig[driverName]; ok {
	    for k, v := range val {
		  newConfig[k] = v
		}
	  }
	}

	return newConfig, nil
}

// fileExists checks if a file exists and is not a directory before we
// try using it to prevent further errors.
func fileExists(filename string) bool {
    info, err := os.Stat(filename)
    if os.IsNotExist(err) {
        return false
    }
    return !info.IsDir()
}

func (l *teeLogger) Log(msg *logger.Message) error {
	errs := []error{}
	for _, lg := range l.loggers {
		// copy message before logging to against resetting.
		// see https://github.com/moby/moby/blob/e4cc3adf81cc0810a416e2b8ce8eb4971e17a3a3/daemon/logger/logger.go#L40
		m := *msg
		if err := lg.logger.Log(&m); err != nil {
			errs = append(errs, err)
		}
		// get message from pool to reduce message pool size
		logger.NewMessage()
	}
	logger.PutMessage(msg)

	if len(errs) != 0 {
		return newMultipleError("faild to log on some loggers", errs)
	}
	return nil
}

func (l *teeLogger) Name() string {
	return pluginName
}

func (l *teeLogger) Close() error {
	errs := []error{}
	for _, lg := range l.loggers {
		if err := lg.logger.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) != 0 {
		return newMultipleError("faild to close on some loggers", errs)
	}
	return nil
}

func (l *teeLogger) ReadLogs(readConfig logger.ReadConfig) *logger.LogWatcher {
	for _, lg := range l.loggers {
		lr, ok := lg.logger.(logger.LogReader)
		if ok {
			return lr.ReadLogs(readConfig)
		}
	}
	return logger.NewLogWatcher()
}
