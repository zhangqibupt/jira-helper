package logger

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/aws/aws-lambda-go/lambdacontext"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

const (
	sizeLimit = 240 * 1024 // CloudWatch log size limit
	// request log type
	requestType = "request"
)

// TODO: @yy remove log for file upload
// logRecord for Request Log
type logRecord struct {
	RequestID       string // AwsRequestID, use as TraceID
	UserID          string // SuperAdmin userID
	Timestamp       int64
	Duration        int64
	HTTPStatusCode  int
	ErrorStackTrace string
	HTTPMethod      string
	RequestPath     string
	RequestQuery    string
	RequestBody     string
	ResponseBody    string
	Headers         map[string][]string
	Type            string `json:"type"` // keyword for logstash to identify the log as request log
}

func (record *logRecord) String() string {
	buf := bytes.NewBufferString("")
	encoder := json.NewEncoder(buf)
	encoder.SetEscapeHTML(false)
	e := encoder.Encode(record)
	if e != nil {
		GetLogger().Error("failed to encode log record", zap.Error(e))
		return "{}"
	}
	return buf.String()
}

// GinLogMiddleware support request log using gin middleware
func GinLogMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		var logRecord *logRecord
		// overwrite the gin.Context.Writer to log response body
		respLogWriter := &respLogWriter{body: bytes.NewBufferString(""), ResponseWriter: c.Writer}
		c.Writer = respLogWriter

		defer func() {
			logStr := logTruncate(logRecord)
			// finally print request log even panic
			fmt.Println(logStr)
			// Remove RequestID
		}()

		defer func() {
			if r := recover(); r != nil {
				stack := string(debug.Stack())
				logRecord.HTTPStatusCode = http.StatusInternalServerError
				logRecord.ErrorStackTrace = stack
				// throw the panic to the later middlewares
				panic(r)
			}
		}()

		logRecord = initLogRecord(c)

		if lc, ok := lambdacontext.FromContext(c.Request.Context()); ok {
			logRecord.RequestID = lc.AwsRequestID
		} else {
			GetLogger().Warn("Can't get AwsRequestID from *gin.Context")
		}

		c.Next()

		// if response normally, fill in remain fields
		logRecord.HTTPStatusCode = c.Writer.Status()
		logRecord.Duration = time.Now().UnixNano()/1e6 - logRecord.Timestamp
		if respLogWriter.body != nil {
			logRecord.ResponseBody = respLogWriter.body.String()
		}
	}
}

func logTruncate(logRecord *logRecord) (logStr string) {
	logStr = logRecord.String()
	if len(logStr) < sizeLimit {
		return logStr
	}
	respSize := len(logRecord.ResponseBody)
	reqSize := len(logRecord.RequestBody)
	// truncate request body or response body if the total size is over the limit
	if len(logStr) > sizeLimit {
		logRecord.ResponseBody = "TRUNCATED..."
	}

	if len(logStr)-respSize > sizeLimit {
		logRecord.RequestBody = "TRUNCATED..."
	}

	if len(logStr)-respSize-reqSize > sizeLimit {
		logRecord.ErrorStackTrace = "TRUNCATED..."
	}
	return logRecord.String()
}

type respLogWriter struct {
	gin.ResponseWriter
	body *bytes.Buffer
}

func (w respLogWriter) Write(b []byte) (int, error) {
	w.body.Write(b)
	return w.ResponseWriter.Write(b)
}

func (w respLogWriter) WriteString(s string) (int, error) {
	w.body.WriteString(s)
	return w.ResponseWriter.WriteString(s)
}

func initLogRecord(ctx *gin.Context) *logRecord {
	var requestBody string
	httpMethod := ctx.Request.Method
	requestPath := ctx.Request.RequestURI
	requestQuery := ctx.Request.URL.Query()
	requestBodyBytes, err := io.ReadAll(ctx.Request.Body)
	if err != nil {

	}
	// reattach request body for later use
	ctx.Request.Body = io.NopCloser(bytes.NewBuffer(requestBodyBytes))
	requestBody = string(requestBodyBytes)

	logRecord := &logRecord{
		Timestamp:    time.Now().UnixNano() / 1e6,
		HTTPMethod:   httpMethod,
		RequestPath:  requestPath,
		RequestQuery: requestQuery.Encode(),
		RequestBody:  requestBody,
		Type:         requestType,
		Headers:      ctx.Request.Header,
	}

	return logRecord
}
