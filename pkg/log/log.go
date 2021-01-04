package log

import (
	"flag"
	"github.com/projectcalico/libcalico-go/lib/logutils"
	"os"

	"github.com/sirupsen/logrus"
)

type LogOptions struct {
	Level int
	File  string
}

func NewLogOptions() *LogOptions {
	return &LogOptions{
		Level: int(logrus.InfoLevel),
		File:  "",
	}
}

func (opt *LogOptions) AddFlags() {
	flag.IntVar(&opt.Level, "log-level", int(logrus.InfoLevel), "set logrus level")
	flag.StringVar(&opt.File, "log-file", "", "set logrus log file")
}

func Setup(opt *LogOptions) {
	logrus.SetLevel(logrus.Level(opt.Level))
	if opt.File != "" {
		f, err := os.OpenFile(opt.File, os.O_CREATE|os.O_WRONLY|os.O_APPEND|os.O_SYNC, 0666)
		if err != nil {
			logrus.Fatalf("failed to open log file %s", opt.File)
		}
		logrus.SetOutput(f)
	}
	// Set up logging formatting.
	logrus.SetFormatter(&logutils.Formatter{})
	// Install a hook that adds file/line no information.
	logrus.AddHook(&logutils.ContextHook{})
}
