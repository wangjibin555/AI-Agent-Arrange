package logger

import (
	"fmt"
	"os"
	"sync"

	midlogger "github.com/wangjibin555/midware/Logger"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var (
	globalLogger *zap.Logger
	defaultMid   midlogger.Logger
	fallbackOnce sync.Once
)

func init() {
	ensureInitialized()
}

// Config holds logger configuration
type Config struct {
	Level  string // debug, info, warn, error
	Format string // json, console
	Output string // stdout, file
	File   string // file path if output is file
}

// Init initializes the global logger through midware/Logger.
func Init(config Config) error {
	writer := zapcore.AddSync(os.Stdout)
	if config.Output == "file" && config.File != "" {
		file, err := os.OpenFile(config.File, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return err
		}
		writer = zapcore.AddSync(file)
	}

	zapConfig := zap.NewProductionConfig()
	if config.Format != "json" {
		zapConfig = zap.NewDevelopmentConfig()
		zapConfig.EncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
	}
	zapConfig.EncoderConfig.TimeKey = "timestamp"
	zapConfig.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	zapConfig.OutputPaths = []string{"stdout"}
	zapConfig.ErrorOutputPaths = []string{"stderr"}
	zapConfig.Level = zap.NewAtomicLevelAt(parseZapLevel(config.Level))

	encoder := zapcore.NewJSONEncoder(zapConfig.EncoderConfig)
	if config.Format != "json" {
		encoder = zapcore.NewConsoleEncoder(zapConfig.EncoderConfig)
	}

	core := zapcore.NewCore(encoder, writer, zapConfig.Level)
	base := zap.New(core, zap.AddCaller(), zap.AddCallerSkip(1), zap.AddStacktrace(zapcore.ErrorLevel))

	adapter := &midwareZapAdapter{logger: base}
	defaultMid = midlogger.New(adapter, midlogger.WithLevel(parseMidwareLevel(config.Level)))
	midlogger.SetDefault(defaultMid)
	globalLogger = base

	return nil
}

func ensureInitialized() {
	if globalLogger != nil && defaultMid != nil {
		return
	}

	fallbackOnce.Do(func() {
		_ = Init(Config{
			Level:  "info",
			Format: "console",
			Output: "stdout",
		})
	})
}

// Get returns the global zap logger for compatibility with existing call sites.
func Get() *zap.Logger {
	ensureInitialized()
	return globalLogger
}

// Info logs an info message.
func Info(msg string, fields ...zap.Field) {
	ensureInitialized()
	if defaultMid != nil {
		defaultMid.Info(msg, convertZapFields(fields)...)
		return
	}
	midlogger.Info(msg, convertZapFields(fields)...)
}

// Debug logs a debug message.
func Debug(msg string, fields ...zap.Field) {
	ensureInitialized()
	if defaultMid != nil {
		defaultMid.Debug(msg, convertZapFields(fields)...)
		return
	}
	midlogger.Debug(msg, convertZapFields(fields)...)
}

// Warn logs a warning message.
func Warn(msg string, fields ...zap.Field) {
	ensureInitialized()
	if defaultMid != nil {
		defaultMid.Warn(msg, convertZapFields(fields)...)
		return
	}
	midlogger.Warn(msg, convertZapFields(fields)...)
}

// Error logs an error message.
func Error(msg string, fields ...zap.Field) {
	ensureInitialized()
	if defaultMid != nil {
		defaultMid.Error(msg, convertZapFields(fields)...)
		return
	}
	midlogger.Error(msg, convertZapFields(fields)...)
}

// Fatal logs a fatal message and exits.
func Fatal(msg string, fields ...zap.Field) {
	ensureInitialized()
	if defaultMid != nil {
		defaultMid.Fatal(msg, convertZapFields(fields)...)
		return
	}
	midlogger.Fatal(msg, convertZapFields(fields)...)
}

// Sync flushes any buffered log entries.
func Sync() {
	if defaultMid != nil {
		_ = defaultMid.Sync()
		return
	}
	if globalLogger != nil {
		_ = globalLogger.Sync()
	}
}

func parseZapLevel(level string) zapcore.Level {
	switch level {
	case "debug":
		return zapcore.DebugLevel
	case "warn":
		return zapcore.WarnLevel
	case "error":
		return zapcore.ErrorLevel
	default:
		return zapcore.InfoLevel
	}
}

func parseMidwareLevel(level string) midlogger.Level {
	switch level {
	case "debug":
		return midlogger.DebugLevel
	case "warn":
		return midlogger.WarnLevel
	case "error":
		return midlogger.ErrorLevel
	default:
		return midlogger.InfoLevel
	}
}

func convertZapFields(fields []zap.Field) []midlogger.Field {
	if len(fields) == 0 {
		return nil
	}

	encoder := zapcore.NewMapObjectEncoder()
	for _, field := range fields {
		field.AddTo(encoder)
	}

	out := make([]midlogger.Field, 0, len(encoder.Fields))
	for key, value := range encoder.Fields {
		switch typed := value.(type) {
		case string:
			out = append(out, midlogger.String(key, typed))
		case int:
			out = append(out, midlogger.Int(key, typed))
		case int64:
			out = append(out, midlogger.Int64(key, typed))
		case bool:
			out = append(out, midlogger.Bool(key, typed))
		case fmt.Stringer:
			out = append(out, midlogger.String(key, typed.String()))
		case error:
			out = append(out, midlogger.Err(key, typed))
		default:
			out = append(out, midlogger.Any(key, typed))
		}
	}

	return out
}

type midwareZapAdapter struct {
	logger *zap.Logger
}

func (a *midwareZapAdapter) Log(level midlogger.Level, msg string, fields []midlogger.Field) {
	zapFields := make([]zap.Field, 0, len(fields))
	for _, field := range fields {
		switch field.Type {
		case midlogger.StringType:
			zapFields = append(zapFields, zap.String(field.Key, field.Value.(string)))
		case midlogger.IntType:
			zapFields = append(zapFields, zap.Int(field.Key, field.Value.(int)))
		case midlogger.Int64Type:
			zapFields = append(zapFields, zap.Int64(field.Key, field.Value.(int64)))
		case midlogger.FloatType:
			zapFields = append(zapFields, zap.Float64(field.Key, field.Value.(float64)))
		case midlogger.BoolType:
			zapFields = append(zapFields, zap.Bool(field.Key, field.Value.(bool)))
		case midlogger.TimeType:
			if value, ok := field.Value.(interface{ String() string }); ok {
				zapFields = append(zapFields, zap.String(field.Key, value.String()))
			} else {
				zapFields = append(zapFields, zap.Any(field.Key, field.Value))
			}
		case midlogger.DurationType:
			zapFields = append(zapFields, zap.Any(field.Key, field.Value))
		case midlogger.ErrorType:
			if err, ok := field.Value.(error); ok {
				zapFields = append(zapFields, zap.NamedError(field.Key, err))
			}
		default:
			zapFields = append(zapFields, zap.Any(field.Key, field.Value))
		}
	}

	switch level {
	case midlogger.TraceLevel, midlogger.DebugLevel:
		a.logger.Debug(msg, zapFields...)
	case midlogger.InfoLevel:
		a.logger.Info(msg, zapFields...)
	case midlogger.WarnLevel:
		a.logger.Warn(msg, zapFields...)
	case midlogger.ErrorLevel:
		a.logger.Error(msg, zapFields...)
	case midlogger.FatalLevel:
		a.logger.Fatal(msg, zapFields...)
	case midlogger.PanicLevel:
		a.logger.Panic(msg, zapFields...)
	default:
		a.logger.Info(msg, zapFields...)
	}
}

func (a *midwareZapAdapter) Sync() error {
	return a.logger.Sync()
}
