package main

import (
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/log"
)

var (
	guilds             = map[string]*Guild{}
	sessions           = map[string]*Session{}
	interrupt          = make(chan os.Signal)
	guildsIndex        = 0
	sameGuildIntervals = map[string]*time.Time{}
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

	signal.Notify(interrupt, os.Interrupt, syscall.SIGTERM, syscall.SIGINT)

	for _, alt := range config.Tokens {
		session := createClient(alt)
		sessions[session.Token] = session
	}

	for _, session := range sessions {
		go session.Connect()
	}

	<-interrupt

	exit()
}

func exit() {
	logger.Infof("Exiting. Terminating %v clients.", len(sessions))

	for _, session := range sessions {
		session.Close()
	}

	logger.Warnf("All connections terminated.")

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
