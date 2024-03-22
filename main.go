package main

import (
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/log"
	jsoniter "github.com/json-iterator/go"
)

var (
	guilds             = map[string]*Guild{}
	sessions           = map[string]*Session{}
	sniping            = map[string]bool{}
	guildsIndex        = 0
	sameGuildIntervals = map[string]*time.Time{}
	json               = jsoniter.ConfigCompatibleWithStandardLibrary
)

var logger = log.NewWithOptions(os.Stderr, log.Options{
	ReportCaller:    false,
	ReportTimestamp: true,
	TimeFormat:      time.TimeOnly,
	Level:           log.DebugLevel,
	Prefix:          "Vanity Sniper",
})

func main() {
	initializeConfig()

	for _, alt := range config.Tokens {
		session := createClient(alt)
		sessions[session.Token] = session
	}

	for _, session := range sessions {
		signal.Notify(session.CloseC, os.Interrupt, syscall.SIGTERM, syscall.SIGINT)

		go func(s *Session) {
			s.Connect()
			<-s.CloseC

			logger.Infof("Closing %v...", strip(s.Token, 35))
			s.CloseWithCode(1000, true)
			logger.Infof("Closed.")

			delete(sessions, s.Token)
		}(session)
	}

	for {
		if len(sessions) == 0 {
			break
		}

	}

	exit()
}

func exit() {
	if len(sessions) != 0 {
		logger.Infof("Exiting. Terminating %v clients.", len(sessions))
	}

	for _, session := range sessions {
		if session.State != "CLOSED" && session.State != "CLOSING" {
			session.Close()
		}
	}

	if len(sessions) != 0 {
		logger.Warnf("All connections terminated.")
	}

	os.Exit(0)
}

func If[T any](cond bool, vtrue, vfalse T) T {
	if cond {
		return vtrue
	}

	return vfalse
}

func strip(content string, length int) string {
	if len(content) < length {
		return content
	}

	return content[0:length] + strings.Repeat("x", len(content)-length)
}

func RemoveIndex(s []int, index int) []int {
	return append(s[:index], s[index+1:]...)
}
