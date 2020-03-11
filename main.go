package main

import (
	"github.com/Sirupsen/logrus"
	"os"
	"os/signal"
	"syscall"
)

const pluginName = "tee"

var log *logrus.Entry

func init() {
	logger := logrus.New()
	logger.SetLevel(logrus.InfoLevel)
	logger.SetFormatter(&logrus.TextFormatter{DisableTimestamp: true})
	log = logger.WithField("pluginName", pluginName)
}

func main() {
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, syscall.SIGHUP)
    d := newDriver()
	h := newHandler(d)
	go func() {
		for {
			s := <-signalChan
			switch s {
				case syscall.SIGHUP:
					log.Info("SIGHUP received, reloading configuration from /etc/docker/tee.json")
					err := d.reload()
					if err != nil {
						log.WithError(err).Errorf("could not re-load configuration!")
					}
			}
		}
	}()
	if err := h.ServeUnix(pluginName, 0); err != nil {
		panic(err)
	}
}
