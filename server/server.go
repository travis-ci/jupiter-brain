package server

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Sirupsen/logrus"
	"github.com/braintree/manners"
	"github.com/codegangsta/negroni"
	"github.com/gorilla/mux"
	"github.com/meatballhat/negroni-logrus"
	"github.com/pkg/errors"
	"github.com/travis-ci/jupiter-brain"
	"github.com/travis-ci/jupiter-brain/metrics"
	"github.com/travis-ci/jupiter-brain/server/jsonapi"
	"github.com/travis-ci/jupiter-brain/server/negroniraven"
	"golang.org/x/net/context"
)

type server struct {
	addr, authToken, sentryDSN string

	log *logrus.Logger

	i jupiterbrain.InstanceManager

	n *negroni.Negroni
	r *mux.Router
	s *manners.GracefulServer

	db       database
	bootTime time.Time
}

func newServer(cfg *Config) (*server, error) {
	log := logrus.New()
	if cfg.Debug {
		log.Level = logrus.DebugLevel
	}

	log.Formatter = &logrus.TextFormatter{DisableColors: true}

	u, err := url.Parse(cfg.VSphereURL)
	if err != nil {
		return nil, errors.Wrap(err, "failed to parse vsphere url")
	}

	if !u.IsAbs() {
		return nil, errors.Errorf("vSphere API URL must be absolute")
	}

	db, err := newPGDatabase(cfg.DatabaseURL)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create postgres database")
	}

	paths := jupiterbrain.VSpherePaths{
		BasePath:    cfg.VSphereBasePath,
		VMPath:      cfg.VSphereVMPath,
		ClusterPath: cfg.VSphereClusterPath,
	}

	srv := &server{
		addr:      cfg.Addr,
		authToken: cfg.AuthToken,
		sentryDSN: cfg.SentryDSN,

		log: log,

		i: jupiterbrain.NewVSphereInstanceManager(log, u, paths),

		n: negroni.New(),
		r: mux.NewRouter(),
		s: manners.NewServer(),

		db:       db,
		bootTime: time.Now().UTC(),
	}

	return srv, nil
}

func (srv *server) Setup() {
	srv.setupRoutes()
	srv.setupMiddleware()
	go srv.signalHandler()
}

func (srv *server) Run() {
	srv.log.WithField("addr", srv.addr).Info("Listening")
	srv.s.Addr = srv.addr
	srv.s.Handler = srv.n
	err := srv.s.ListenAndServe()
	if err != nil {
		srv.log.WithField("err", err).Error("ListenAndServe failed")
	}
}

func (srv *server) setupRoutes() {
	srv.r.HandleFunc(`/instances`, srv.handleInstancesList).Methods("GET").Name("instances-list")
	srv.r.HandleFunc(`/instances`, srv.handleInstancesCreate).Methods("POST").Name("instances-create")
	srv.r.HandleFunc(`/instances/{id}`, srv.handleInstanceByIDFetch).Methods("GET").Name("instance-by-id")
	srv.r.HandleFunc(`/instances/{id}`, srv.handleInstanceByIDTerminate).Methods("DELETE").Name("instance-by-id-terminate")
	srv.r.HandleFunc(`/instance-syncs`, srv.handleInstanceSync).Methods("POST").Name("instance-syncs-create")
}

func (srv *server) setupMiddleware() {
	srv.n.Use(negroni.NewRecovery())
	srv.n.Use(negronilogrus.NewCustomMiddleware(srv.log.Level, srv.log.Formatter, "web"))
	srv.n.Use(negroni.HandlerFunc(srv.authMiddleware))
	nr, err := negroniraven.NewMiddleware(srv.sentryDSN)
	if err != nil {
		panic(err)
	}
	srv.n.Use(nr)
	srv.n.UseHandler(srv.r)
}

func (srv *server) authMiddleware(w http.ResponseWriter, req *http.Request, f http.HandlerFunc) {
	authHeader := req.Header.Get("Authorization")
	srv.log.WithField("authorization", authHeader).Debug("raw authorization header")

	if authHeader == "" {
		w.Header().Set("WWW-Authenticate", "token")
		srv.log.WithField("request_id", req.Header.Get("X-Request-ID")).Debug("responding 401 due to empty Authorization header")

		jsonapi.Error(w, errors.New("token is required"), http.StatusUnauthorized)
		return
	}

	if strings.HasPrefix(authHeader, "token ") && subtle.ConstantTimeCompare([]byte("token "+srv.authToken), []byte(authHeader)) == 1 {
		f(w, req)
		return
	} else if strings.HasPrefix(authHeader, "token=") && subtle.ConstantTimeCompare([]byte("token="+srv.authToken), []byte(authHeader)) == 1 {
		f(w, req)
		return
	}

	jsonapi.Error(w, errors.New("incorrect token"), http.StatusUnauthorized)
}

func (srv *server) handleInstancesList(w http.ResponseWriter, req *http.Request) {
	defer metrics.TimeSince("travis.jupiter-brain.endpoints.instances-list", time.Now())

	instances, err := srv.i.List(context.TODO())
	if err != nil {
		jsonapi.Error(w, err, http.StatusInternalServerError)
		return
	}

	dbInstanceIDs := []string{}
	dbInstanceIDCreatedMap := map[string]time.Time{}
	applyDBFilter := false

	if req.FormValue("min_age") != "" {
		dur, err := time.ParseDuration(req.FormValue("min_age"))
		if err != nil {
			jsonapi.Error(w, err, http.StatusBadRequest)
			return
		}

		res, err := srv.db.FetchInstances(&databaseQuery{MinAge: dur})
		if err != nil {
			jsonapi.Error(w, err, http.StatusBadRequest)
			return
		}

		srv.log.WithFields(logrus.Fields{
			"n": len(res),
		}).Debug("retrieved instances from database")

		for _, r := range res {
			dbInstanceIDCreatedMap[r.ID] = r.CreatedAt
			dbInstanceIDs = append(dbInstanceIDs, r.ID)
		}

		applyDBFilter = true
	}

	response := map[string][]interface{}{
		"data": make([]interface{}, 0),
	}

	if applyDBFilter {
		keptInstances := []*jupiterbrain.Instance{}
		for _, instance := range instances {
			for _, instID := range dbInstanceIDs {
				if instID == instance.ID {
					instance.CreatedAt = dbInstanceIDCreatedMap[instID]
					keptInstances = append(keptInstances, instance)
				}
			}
		}

		srv.log.WithFields(logrus.Fields{
			"pre_filter":  len(instances),
			"post_filter": len(keptInstances),
		}).Debug("applying known instance filter")

		instances = keptInstances
	}

	for _, instance := range instances {
		response["data"] = append(response["data"], MarshalInstance(instance))
	}

	b, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		jsonapi.Error(w, err, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/vnd.api+json")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, string(b)+"\n")
}

func (srv *server) handleInstancesCreate(w http.ResponseWriter, req *http.Request) {
	defer metrics.TimeSince("travis.jupiter-brain.endpoints.instances-create", time.Now())

	var requestBody map[string]map[string]string

	err := json.NewDecoder(req.Body).Decode(&requestBody)
	if err != nil {
		jsonapi.Error(w, err, http.StatusBadRequest)
		return
	}

	if requestBody["data"] == nil {
		jsonapi.Error(w, &jsonapi.JSONError{Status: "422", Code: "missing-field", Title: "root object must have data field"}, 422)
		return
	}

	if requestBody["data"]["type"] != "instances" {
		jsonapi.Error(w, &jsonapi.JSONError{Status: "409", Code: "incorrect-type", Title: "data must be of type instances"}, http.StatusConflict)
		return
	}

	if requestBody["data"]["base-image"] == "" {
		jsonapi.Error(w, &jsonapi.JSONError{Status: "422", Code: "missing-field", Title: "instance must have base-image field"}, 422)
		return
	}

	instance, err := srv.i.Start(context.TODO(), requestBody["data"]["base-image"])
	if err != nil {
		jsonapi.Error(w, err, http.StatusInternalServerError)
		return
	}

	recoverDelete := false
	defer func() {
		if recoverDelete && instance != nil {
			go func() { _ = srv.i.Terminate(context.TODO(), instance.ID) }()
		}
	}()

	instance.CreatedAt = time.Now().UTC()
	err = srv.db.SaveInstance(instance)
	if err != nil {
		recoverDelete = true
		jsonapi.Error(w, err, http.StatusInternalServerError)
		return
	}

	response := map[string][]interface{}{
		"data": {MarshalInstance(instance)},
	}

	b, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		recoverDelete = true
		jsonapi.Error(w, err, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/vnd.api+json")
	w.Header().Set("Location", fmt.Sprintf("/instances/%s", instance.ID))
	w.WriteHeader(http.StatusCreated)
	fmt.Fprintf(w, string(b)+"\n")
}

func (srv *server) handleInstanceByIDFetch(w http.ResponseWriter, req *http.Request) {
	defer metrics.TimeSince("travis.jupiter-brain.endpoints.instance-by-id-fetch", time.Now())

	vars := mux.Vars(req)
	instance, err := srv.i.Fetch(context.TODO(), vars["id"])
	if err != nil {
		switch err.(type) {
		case jupiterbrain.VirtualMachineNotFoundError:
			jsonapi.Error(w, err, http.StatusNotFound)
			return
		default:
			srv.log.WithFields(logrus.Fields{
				"err": err,
				"id":  vars["id"],
			}).Error("failed to fetch instance")
			jsonapi.Error(w, err, http.StatusInternalServerError)
			return
		}
	}

	response := map[string][]interface{}{
		"data": {MarshalInstance(instance)},
	}

	b, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		jsonapi.Error(w, err, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/vnd.api+json")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, string(b)+"\n")
}

func (srv *server) handleInstanceByIDTerminate(w http.ResponseWriter, req *http.Request) {
	defer metrics.TimeSince("travis.jupiter-brain.endpoints.instance-by-id-terminate", time.Now())

	vars := mux.Vars(req)
	err := srv.i.Terminate(context.TODO(), vars["id"])
	if err != nil {
		switch err.(type) {
		case jupiterbrain.VirtualMachineNotFoundError:
			jsonapi.Error(w, err, http.StatusNotFound)
			return
		default:
			srv.log.WithFields(logrus.Fields{
				"err": err,
				"id":  vars["id"],
			}).Error("failed to terminate instance")
			jsonapi.Error(w, err, http.StatusInternalServerError)
			return
		}
	}

	err = srv.db.DestroyInstance(vars["id"])
	if err != nil {
		jsonapi.Error(w, err, http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (srv *server) handleInstanceSync(w http.ResponseWriter, req *http.Request) {
	defer metrics.TimeSince("travis.jupiter-brain.endpoints.instance-sync", time.Now())
	instances, err := srv.i.List(context.TODO())
	if err != nil {
		jsonapi.Error(w, err, http.StatusInternalServerError)
		return
	}

	for _, instance := range instances {
		instance.CreatedAt = time.Now().UTC()
		err = srv.db.SaveInstance(instance)
		if err != nil {
			srv.log.WithFields(logrus.Fields{
				"err": err,
				"id":  instance.ID,
			}).Warn("failed to save instance")
			continue
		}

		srv.log.WithField("id", instance.ID).Debug("synced instance")
	}

	w.WriteHeader(http.StatusNoContent)
}

func (srv *server) signalHandler() {
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, syscall.SIGTERM, syscall.SIGINT, syscall.SIGUSR1)
	for {
		select {
		case sig := <-signalChan:
			switch sig {
			case syscall.SIGTERM:
				srv.log.Info("Received SIGTERM, shutting down now.")
				srv.s.Close()
			case syscall.SIGINT:
				srv.log.Info("Received SIGINT, shutting down now.")
				srv.s.Close()
			case syscall.SIGUSR1:
				srv.log.WithFields(logrus.Fields{
					"version":   os.Getenv("VERSION"),
					"revision":  os.Getenv("REVISION"),
					"boot_time": srv.bootTime,
					"uptime":    time.Since(srv.bootTime),
				}).Info("Received SIGUSR1.")
			default:
				log.Print("ignoring unknown signal")
			}
		default:
			time.Sleep(time.Second)
		}
	}
}
