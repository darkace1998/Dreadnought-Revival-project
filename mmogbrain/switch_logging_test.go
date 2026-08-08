package main

import (
	"strings"
	"testing"

	"github.com/sirupsen/logrus"
)

type capture struct{ b strings.Builder }

func (c *capture) Write(p []byte) (int, error) { return c.b.Write(p) }

// An A/B against a running server has twice measured nothing without saying so:
// once because a switch is read inside a sync.Once and needs a restart, once
// because the binary predated the change and the variable was never set. Both
// were invisible from outside the process. The startup line exists so "did the
// experiment actually run?" is answerable from the log.
func TestStartupSaysWhenASwitchIsActive(t *testing.T) {
	t.Setenv("DN_TECHTREE_SELF_CLASSID", "1")
	log := logrus.New()
	cap := &capture{}
	log.SetOutput(cap)
	log.SetFormatter(&logrus.JSONFormatter{})

	logTechTreeSwitches(log)
	out := cap.b.String()

	if !strings.Contains(out, "DN_TECHTREE_SELF_CLASSID") {
		t.Errorf("startup log does not name the active switch: %s", out)
	}
	if !strings.Contains(out, "ACTIVE") {
		t.Errorf("an active switch must be logged loudly, got: %s", out)
	}
	// The build stamp is the other half: a switch cannot take effect in a
	// binary that predates it.
	if !strings.Contains(out, "build") {
		t.Errorf("startup log carries no build stamp: %s", out)
	}
}

func TestStartupSaysWhenEverythingIsDefault(t *testing.T) {
	for _, name := range techTreeSwitches {
		t.Setenv(name, "")
	}
	log := logrus.New()
	cap := &capture{}
	log.SetOutput(cap)
	log.SetFormatter(&logrus.JSONFormatter{})

	logTechTreeSwitches(log)
	out := cap.b.String()

	if !strings.Contains(out, "all default") {
		t.Errorf("expected a plain default line, got: %s", out)
	}
	if strings.Contains(out, "ACTIVE") {
		t.Errorf("no switch is set but the log claims one is: %s", out)
	}
}
