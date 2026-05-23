package logging

import (
	"os"

	"github.com/sirupsen/logrus"
)

// serviceHook injects the "service" field into every log entry produced by
// loggers returned from New, so callers don't need to call WithField("service", ...)
// on each log line.
type serviceHook struct {
	service string
}

func (h *serviceHook) Levels() []logrus.Level {
	return logrus.AllLevels
}

func (h *serviceHook) Fire(entry *logrus.Entry) error {
	entry.Data["service"] = h.service
	return nil
}

// New returns a logrus logger configured with JSON output and the given
// service name injected into every log entry.
func New(service string) *logrus.Logger {
	log := logrus.New()
	log.SetFormatter(&logrus.JSONFormatter{})
	log.SetOutput(os.Stdout)
	log.SetLevel(logrus.InfoLevel)
	log.AddHook(&serviceHook{service: service})
	return log
}

// Entry returns a pre-populated entry with the service field set.
func Entry(log *logrus.Logger, service string) *logrus.Entry {
	return log.WithField("service", service)
}
