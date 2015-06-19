package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"github.com/Sirupsen/logrus"
	"github.com/braintree/manners"
	"github.com/codegangsta/negroni"
	"github.com/gorilla/mux"
	"github.com/meatballhat/negroni-logrus"
	"github.com/travis-ci/jupiter-brain"
	"github.com/travis-ci/jupiter-brain/server/jsonapi"
	"golang.org/x/net/context"
)

type server struct {
	addr, authToken, sentryDSN string

	log *logrus.Logger

	i jupiterbrain.InstanceManager

	n *negroni.Negroni
	r *mux.Router
	s *manners.GracefulServer
}

func newServer(cfg *Config) (*server, error) {
	log := logrus.New()
	if cfg.Debug {
		log.Level = logrus.DebugLevel
	}

	logrus.SetFormatter(&logrus.TextFormatter{DisableColors: true})

	u, err := url.Parse(cfg.VSphereURL)
	if err != nil {
		return nil, err
	}

	if !u.IsAbs() {
		return nil, fmt.Errorf("vSphere API URL must be absolute")
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
	}

	return srv, nil
}

func (srv *server) Setup() {
	srv.setupRoutes()
	srv.setupMiddleware()
}

func (srv *server) Run() {
	srv.log.WithField("addr", srv.addr).Info("Listening")
	_ = srv.s.ListenAndServe(srv.addr, srv.n)
}

func (srv *server) setupRoutes() {
	srv.r.HandleFunc(`/instances`, srv.handleInstancesList).Methods("GET").Name("instances-list")
	srv.r.HandleFunc(`/instances`, srv.handleInstancesCreate).Methods("POST").Name("instances-create")
	srv.r.HandleFunc(`/instances/{id}`, srv.handleInstanceByIDFetch).Methods("GET").Name("instance-by-id")
	srv.r.HandleFunc(`/instances/{id}`, srv.handleInstanceByIDTerminate).Methods("DELETE").Name("instance-by-id-terminate")
}

func (srv *server) setupMiddleware() {
	srv.n.Use(negroni.NewRecovery())
	srv.n.Use(negronilogrus.NewMiddleware())
	srv.n.Use(negroni.HandlerFunc(srv.authMiddleware))
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

	if authHeader != ("token "+srv.authToken) && authHeader != ("token="+srv.authToken) {
		jsonapi.Error(w, errors.New("incorrect token"), http.StatusUnauthorized)
		return
	}

	f(w, req)
}

func (srv *server) handleInstancesList(w http.ResponseWriter, req *http.Request) {
	instances, err := srv.i.List(context.TODO())
	if err != nil {
		jsonapi.Error(w, err, http.StatusInternalServerError)
		return
	}

	response := map[string][]interface{}{
		"data": make([]interface{}, 0),
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

	response := map[string][]interface{}{
		"data": {MarshalInstance(instance)},
	}

	b, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		jsonapi.Error(w, err, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/vnd.api+json")
	w.Header().Set("Location", fmt.Sprintf("/instances/%s", instance.ID))
	w.WriteHeader(http.StatusCreated)
	fmt.Fprintf(w, string(b)+"\n")
}

func (srv *server) handleInstanceByIDFetch(w http.ResponseWriter, req *http.Request) {
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

	w.WriteHeader(http.StatusNoContent)
	return
}
