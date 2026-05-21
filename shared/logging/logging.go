package logging

import (
	"os"

	"github.com/sirupsen/logrus"
)

// New returns a logrus logger configured with JSON output and the given
// service name injected as a field.
func New(service string) *logrus.Logger {
	log := logrus.New()
	log.SetFormatter(&logrus.JSONFormatter{})
	log.SetOutput(os.Stdout)
	log.SetLevel(logrus.InfoLevel)
	log.WithField("service", service)
	return log
}

// Entry returns a pre-populated entry with the service field set.
func Entry(log *logrus.Logger, service string) *logrus.Entry {
	return log.WithField("service", service)
}
