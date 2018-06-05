package arklogrus

import (
	"io"

	"github.com/heptio/ark/pkg/logger"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

type Option func(log *logrus.Logger)

func Out(w io.Writer) Option {
	return func(log *logrus.Logger) {
		log.Out = w
	}
}

func Hook(hook logrus.Hook) Option {
	return func(log *logrus.Logger) {
		log.Hooks.Add(hook)
	}
}

func Formatter(formatter logrus.Formatter) Option {
	return func(log *logrus.Logger) {
		log.Formatter = formatter
	}
}

func Level(level logger.Level) Option {
	return func(log *logrus.Logger) {
		switch level {
		case logger.FatalLevel:
			log.Level = logrus.FatalLevel
		case logger.ErrorLevel:
			log.Level = logrus.ErrorLevel
		case logger.WarnLevel:
			log.Level = logrus.WarnLevel
		case logger.InfoLevel:
			log.Level = logrus.InfoLevel
		case logger.DebugLevel:
			log.Level = logrus.DebugLevel
		default:
			panic(errors.Errorf("invalid level %v", level))
		}
	}
}

func New(options ...Option) logger.Interface {
	log := logrus.New()

	for _, option := range options {
		option(log)
	}

	l := &logrusLogger{
		entry: logrus.NewEntry(log),
	}

	return l
}

type logrusLogger struct {
	entry *logrus.Entry
}

func (l *logrusLogger) Level() logger.Level {
	switch l.entry.Level {
	case logrus.PanicLevel, logrus.FatalLevel:
		return logger.FatalLevel
	case logrus.ErrorLevel:
		return logger.ErrorLevel
	case logrus.WarnLevel:
		return logger.WarnLevel
	case logrus.InfoLevel:
		return logger.InfoLevel
	case logrus.DebugLevel:
		return logger.DebugLevel
	}

	panic(errors.Errorf("invalid level %v", l.entry.Level))
}

func (l *logrusLogger) WithFields(fields ...interface{}) logger.Interface {
	if len(fields)%2 != 0 {
		panic("incorrect use of WithFields: an even number of function arguments is required")
	}

	logrusFields := make(logrus.Fields, len(fields)/2)
	for i := 0; i < len(fields); i += 2 {
		key, ok := fields[i].(string)
		if !ok {
			panic(errors.Errorf("key %#v must be a string", fields[i]))
		}
		value := fields[i+1]
		logrusFields[key] = value
	}

	return &logrusLogger{
		entry: l.entry.WithFields(logrusFields),
	}
}

func (l *logrusLogger) WithError(err error) logger.Interface {
	return &logrusLogger{
		entry: l.entry.WithError(err),
	}
}

func (l *logrusLogger) Debugf(format string, args ...interface{}) {
	l.entry.Debugf(format, args...)
}

func (l *logrusLogger) Infof(format string, args ...interface{}) {
	l.entry.Infof(format, args...)
}

func (l *logrusLogger) Printf(format string, args ...interface{}) {
	l.entry.Printf(format, args...)
}

func (l *logrusLogger) Warnf(format string, args ...interface{}) {
	l.entry.Warnf(format, args...)
}

func (l *logrusLogger) Errorf(format string, args ...interface{}) {
	l.entry.Errorf(format, args...)
}

func (l *logrusLogger) Fatalf(format string, args ...interface{}) {
	l.entry.Fatalf(format, args...)
}

func (l *logrusLogger) Debug(args ...interface{}) {
	l.entry.Debug(args...)
}

func (l *logrusLogger) Info(args ...interface{}) {
	l.entry.Info(args...)
}

func (l *logrusLogger) Print(args ...interface{}) {
	l.entry.Print(args...)
}

func (l *logrusLogger) Warn(args ...interface{}) {
	l.entry.Warn(args...)
}

func (l *logrusLogger) Error(args ...interface{}) {
	l.entry.Error(args...)
}

func (l *logrusLogger) Fatal(args ...interface{}) {
	l.entry.Fatal(args...)
}

func (l *logrusLogger) Debugln(args ...interface{}) {
	l.entry.Debugln(args...)
}

func (l *logrusLogger) Infoln(args ...interface{}) {
	l.entry.Infoln(args...)
}

func (l *logrusLogger) Println(args ...interface{}) {
	l.entry.Println(args...)
}

func (l *logrusLogger) Warnln(args ...interface{}) {
	l.entry.Warnln(args...)
}

func (l *logrusLogger) Errorln(args ...interface{}) {
	l.entry.Errorln(args...)
}

func (l *logrusLogger) Fatalln(args ...interface{}) {
	l.entry.Fatalln(args...)
}
