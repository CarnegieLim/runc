// Copyright 2013, Cong Ding. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// author: Cong Ding <dinggnu@gmail.com>

// Package logging implements log library for other applications. It provides
// functions Debug, Info, Warning, Error, Critical, and formatting version
// Logf.
//
// Example:
//
//	logger := logging.SimpleLogger("main")
//	logger.SetLevel(logging.WARNING)
//	logger.Error("test for error")
//	logger.Warning("test for warning", "second parameter")
//	logger.Debug("test for debug")
//
package logging

import (
	"github.com/ccding/go-config-reader/config"
	"io"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// Pre-defined formats
const (
	DefaultFileName     = "logging.log"                   // default logging filename
	DefaultTimeFormat   = "2006-01-02 15:04:05.999999999" // defaulttime format
	DefaultBufferSize   = 1000                            // default buffer size for writer
	DefaultQueueSize    = 10000                           // default chan queue size in async logging
	DefaultRequestSize  = 10000                           // default chan queue size in async logging
	DefaultTimeInterval = 100                             // default time interval in milliseconds in async logging
)

// Logger is the logging struct.
type Logger struct {

	// Be careful of the alignment issue of the variable seqid because it
	// uses the sync/atomic.AddUint64() operation. If the alignment is
	// wrong, it will cause a panic. To solve the alignment issue in an
	// easy way, we put seqid to the beginning of the structure.
	// seqid is only visiable internally.
	seqid uint64 // last used sequence number in record

	// These variables can be configured by users.
	name         string    // logger name
	level        Level     // record level higher than this will be printed
	recordFormat string    // format of the record
	recordArgs   []string  // arguments to be used in the recordFormat
	out          io.Writer // writer
	sync         bool      // use sync or async way to record logs
	timeFormat   string    // format for time

	// These variables are visible to users.
	startTime time.Time // start time of the logger

	// Internally used variables, which don't have get and set functions.
	wlock   sync.Mutex   // writer lock
	queue   chan string  // queue used in async logging
	request chan request // queue used in non-runtime logging
	flush   chan bool    // flush signal for the watcher to write
	finish  chan bool    // finish flush signal for the flush function to return
	quit    chan bool    // quit signal for the watcher to quit
	fd      *os.File     // file handler, used to close the file on destroy
	runtime bool         // with runtime operation or not

	// The customized configurations.
	bufferSize   int
	timeInterval time.Duration
}

// SimpleLogger creates a new logger with simple configuration.
func SimpleLogger(name string) (*Logger, error) {
	return createLogger(name, WARNING, BasicFormat, DefaultTimeFormat, os.Stdout, false)
}

// BasicLogger creates a new logger with basic configuration.
func BasicLogger(name string) (*Logger, error) {
	return FileLogger(name, WARNING, BasicFormat, DefaultTimeFormat, DefaultFileName, false)
}

// RichLogger creates a new logger with simple configuration.
func RichLogger(name string) (*Logger, error) {
	return FileLogger(name, NOTSET, RichFormat, DefaultTimeFormat, DefaultFileName, false)
}

// FileLogger creates a new logger with file output.
func FileLogger(name string, level Level, format string, timeFormat string, file string, sync bool) (*Logger, error) {
	out, err := os.OpenFile(file, os.O_WRONLY|os.O_APPEND|os.O_CREATE, os.ModeAppend|0644)
	if err != nil {
		return nil, err
	}
	logger, err := createLogger(name, level, format, timeFormat, out, sync)
	if err == nil {
		logger.fd = out
		return logger, nil
	} else {
		return nil, err
	}
}

// WriterLogger creates a new logger with a writer
func WriterLogger(name string, level Level, format string, timeFormat string, out io.Writer, sync bool) (*Logger, error) {
	return createLogger(name, level, format, timeFormat, out, sync)
}

// CustomizedLogger creates a new logger with all configurations customized
// (in addition to WriterLogger).
func CustomizedLogger(name string, level Level, format string, timeFormat string, out io.Writer, sync bool, queueSize int, requestSize int, bufferSize int, timeInterval time.Duration) (*Logger, error) {
	return createCustomizedLogger(name, level, format, timeFormat, out, sync, queueSize, requestSize, bufferSize, timeInterval)
}

// ConfigLogger creates a new logger from a configuration file
func ConfigLogger(filename string) (*Logger, error) {
	conf := config.NewConfig(filename)
	err := conf.Read()
	if err != nil {
		return nil, err
	}
	name := conf.Get("", "name")
	slevel := conf.Get("", "level")
	if slevel == "" {
		slevel = "0"
	}
	l, err := strconv.Atoi(slevel)
	if err != nil {
		return nil, err
	}
	level := Level(l)
	format := conf.Get("", "format")
	if format == "" {
		format = BasicFormat
	}
	timeFormat := conf.Get("", "timeFormat")
	if timeFormat == "" {
		timeFormat = DefaultTimeFormat
	}
	ssync := conf.Get("", "sync")
	if ssync == "" {
		ssync = "0"
	}
	file := conf.Get("", "file")
	if file == "" {
		file = DefaultFileName
	}
	sync := true
	if ssync == "0" {
		sync = false
	} else if ssync == "1" {
		sync = true
	} else {
		return nil, err
	}
	return FileLogger(name, level, format, timeFormat, file, sync)
}

// createLogger create a new logger
func createLogger(name string, level Level, format string, timeFormat string, out io.Writer, sync bool) (*Logger, error) {
	return createCustomizedLogger(name, level, format, timeFormat, out, sync, DefaultQueueSize, DefaultRequestSize, DefaultBufferSize, DefaultTimeInterval)
}

// createCustomizedLogger create a new logger with customizing queue size and request size
func createCustomizedLogger(name string, level Level, format string, timeFormat string, out io.Writer, sync bool, queueSize int, requestSize int, bufferSize int, timeInterval time.Duration) (*Logger, error) {
	logger := new(Logger)

	err := logger.parseFormat(format)
	if err != nil {
		return nil, err
	}

	// assign values to logger
	logger.name = name
	logger.level = level
	logger.out = out
	logger.seqid = 0
	logger.sync = sync
	logger.queue = make(chan string, queueSize)
	logger.request = make(chan request, requestSize)
	logger.flush = make(chan bool)
	logger.finish = make(chan bool)
	logger.quit = make(chan bool)
	logger.startTime = time.Now()
	logger.fd = nil
	logger.timeFormat = timeFormat
	logger.bufferSize = bufferSize
	logger.timeInterval = timeInterval

	// start watcher to write logs if it is async or no runtime field
	if !logger.sync {
		go logger.watcher()
	}

	return logger, nil
}

// Destroy sends quit signal to watcher and releases all the resources.
func (logger *Logger) Destroy() {
	if !logger.sync {
		// quit watcher
		logger.quit <- true
		// wait for watcher quit
		<-logger.quit
	}
	// clean up
	if logger.fd != nil {
		logger.fd.Close()
	}
}

// Flush the writer
func (logger *Logger) Flush() {
	if !logger.sync {
		// send flush signal
		logger.flush <- true
		// wait for the flush finish
		<-logger.finish
	}
}

// Getter functions

func (logger *Logger) Name() string {
	return logger.name
}

func (logger *Logger) StartTime() int64 {
	return logger.startTime.UnixNano()
}

func (logger *Logger) TimeFormat() string {
	return logger.timeFormat
}

func (logger *Logger) Level() Level {
	return Level(atomic.LoadInt32((*int32)(&logger.level)))
}

func (logger *Logger) RecordFormat() string {
	return logger.recordFormat
}

func (logger *Logger) RecordArgs() []string {
	return logger.recordArgs
}

func (logger *Logger) Writer() io.Writer {
	return logger.out
}

func (logger *Logger) Sync() bool {
	return logger.sync
}

// Setter functions

func (logger *Logger) SetLevel(level Level) {
	atomic.StoreInt32((*int32)(&logger.level), int32(level))
}

func (logger *Logger) SetWriter(out ...io.Writer) {
	logger.out = io.MultiWriter(out...)
}
