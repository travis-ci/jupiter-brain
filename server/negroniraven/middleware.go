package negroniraven

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/Sirupsen/logrus"
	"github.com/getsentry/raven-go"
)

type Middleware struct {
	cl  *raven.Client
	log *logrus.Logger
}

func NewMiddleware(sentryDSN, environment string) (*Middleware, error) {
	cl, err := raven.NewClient(sentryDSN, SentryTags)
	if err != nil {
		return nil, err
	}

	if environment != "" {
		cl.SetEnvironment(environment)
	}

	log := logrus.New()
	log.Formatter = &logrus.TextFormatter{DisableColors: true}
	return &Middleware{cl: cl, log: log}, nil
}

func (mw *Middleware) ServeHTTP(w http.ResponseWriter, req *http.Request, next http.HandlerFunc) {
	defer func() {
		var packet *raven.Packet

		p := recover()
		switch rval := p.(type) {
		case nil:
			return
		case error:
			packet = raven.NewPacket(rval.Error(), raven.NewException(rval, raven.NewStacktrace(2, 3, nil)), raven.NewHttp(req))
		case *logrus.Entry:
			entryErrInterface, ok := rval.Data["err"]
			if !ok {
				entryErrInterface = fmt.Errorf(rval.Message)
			}

			entryErr, ok := entryErrInterface.(error)
			if !ok {
				entryErr = fmt.Errorf(rval.Message)
			}

			packet = raven.NewPacket(rval.Message, raven.NewException(entryErr, raven.NewStacktrace(2, 3, nil)), raven.NewHttp(req))
		default:
			rvalStr := fmt.Sprint(rval)
			packet = raven.NewPacket(rvalStr, raven.NewException(errors.New(rvalStr), raven.NewStacktrace(2, 3, nil)), raven.NewHttp(req))
		}

		SendRavenPacket(packet, mw.cl, mw.log, nil)
		panic(p)
	}()

	next(w, req)
}
