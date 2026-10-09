package logger

import (
	"encoding/json"
	"os"
	"time"
)

type Logger struct {
	out *json.Encoder
	err *json.Encoder
}

func NewLogger() *Logger {
	return &Logger{
		out: json.NewEncoder(os.Stdout),
		err: json.NewEncoder(os.Stderr),
	}
}

func (l *Logger) log(enc *json.Encoder, level, msg string, kv ...any) {
	fields := map[string]interface{}{
		"ts":    time.Now().UTC().Format(time.RFC3339Nano),
		"level": level,
		"msg":   msg,
	}
	for i := 0; i+1 < len(kv); i += 2 {
		k, ok := kv[i].(string)
		if !ok {
			continue
		}
		v := kv[i+1]
		// errors serialize as {} under encoding/json — render the message.
		if err, ok := v.(error); ok {
			v = err.Error()
		}
		fields[k] = v
	}
	_ = enc.Encode(fields)
}

func (l *Logger) Info(msg string, kv ...any) {
	l.log(l.out, "info", msg, kv...)
}

func (l *Logger) Error(msg string, kv ...any) {
	l.log(l.err, "error", msg, kv...)
}

func (l *Logger) Fatal(msg string, kv ...any) {
	l.log(l.err, "fatal", msg, kv...)
	os.Exit(1)
}
