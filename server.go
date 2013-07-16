package socketio

import (
	"code.google.com/p/go.net/websocket"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
)

var (
	uriRegexp = regexp.MustCompile(`^(.+?)/(1)(?:/([^/]+)/([^/]+))?/?$`)
)

type Config struct {
	HeartbeatTimeout int
	ClosingTimeout   int
	NewSessionID     func() string
	Transports       *TransportManager
	Authorize        func(*http.Request) bool
}

type SocketIOServer struct {
	mutex            sync.RWMutex
	heartbeatTimeout int
	closingTimeout   int
	authorize        func(*http.Request) bool
	newSessionId     func() string
	transports       *TransportManager
	sessions         map[string]*Session
	eventEmitters    map[string]*EventEmitter
}

func NewSocketIOServer(config *Config) *SocketIOServer {
	server := new(SocketIOServer)
	if config != nil {
		server.heartbeatTimeout = config.HeartbeatTimeout
		server.closingTimeout = config.ClosingTimeout
		server.newSessionId = config.NewSessionID
		server.transports = config.Transports
		server.authorize = config.Authorize
	}
	if server.heartbeatTimeout == 0 {
		server.heartbeatTimeout = 15000
	}
	if server.closingTimeout == 0 {
		server.closingTimeout = 10000
	}
	if server.newSessionId == nil {
		server.newSessionId = NewSessionID
	}
	if server.transports == nil {
		server.transports = DefaultTransports
	}
	server.sessions = make(map[string]*Session)
	server.eventEmitters = make(map[string]*EventEmitter)
	return server
}

func (srv *SocketIOServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if !strings.HasPrefix(path, "/socket.io/1/") {
		http.NotFound(w, r)
		return
	}
	path = path[len("/socket.io/1/"):]
	if path == "" {
		srv.handShake(w, r)
		return
	}

	spliter := strings.SplitN(path, "/", 2)
	if len(spliter) < 2 {
		http.NotFound(w, r)
		return
	}

	transportName, sessionId := spliter[0], spliter[1]
	if transportName != "websocket" {
		http.Error(w, "not websocket", http.StatusBadRequest)
		return
	}

	session := srv.getSession(sessionId)
	if session == nil {
		http.Error(w, "invalid session id", http.StatusBadRequest)
		return
	}
	defer srv.removeSession(session)

	// open
	transport := newWebSocket(session, srv.heartbeatTimeout)

	websocket.Handler(transport.webSocketHandler).ServeHTTP(w, r)
}

func (srv *SocketIOServer) Of(name string) *EventEmitter {
	ret, ok := srv.eventEmitters[name]
	if !ok {
		ret = NewEventEmitter()
		srv.eventEmitters[name] = ret
	}
	return ret
}

func (srv *SocketIOServer) On(name string, fn interface{}) error {
	return srv.Of("").On(name, fn)
}

func (srv *SocketIOServer) RemoveListener(name string, fn interface{}) {
	srv.Of("").RemoveListener(name, fn)
}

func (srv *SocketIOServer) RemoveAllListeners(name string) {
	srv.Of("").RemoveAllListeners(name)
}

// authorize origin!!
func (srv *SocketIOServer) handShake(w http.ResponseWriter, r *http.Request) {
	if srv.authorize != nil {
		if ok := srv.authorize(r); !ok {
			http.Error(w, "", 401)
			return
		}
	}
	w.Header().Set("Access-Control-Allow-Origin", r.Header.Get("origin"))
	w.Header().Set("Access-Control-Allow-Methods", "GET")
	w.Header().Set("Access-Control-Allow-Credentials", "true")
	sessionId := NewSessionID()
	if sessionId == "" {
		http.Error(w, "", 503)
		return
	}
	transportNames := srv.transports.GetTransportNames()
	fmt.Fprintf(w, "%s:%d:%d:%s",
		sessionId,
		srv.heartbeatTimeout,
		srv.closingTimeout,
		strings.Join(transportNames, ","))
	session := NewSession(srv.eventEmitters, sessionId, srv.heartbeatTimeout)
	srv.addSession(session)
}

func (srv *SocketIOServer) addSession(ss *Session) {
	srv.mutex.Lock()
	defer srv.mutex.Unlock()
	srv.sessions[ss.SessionId] = ss
}

func (srv *SocketIOServer) removeSession(ss *Session) {
	srv.mutex.Lock()
	defer srv.mutex.Unlock()
	delete(srv.sessions, ss.SessionId)
}

func (srv *SocketIOServer) getSession(sessionId string) *Session {
	srv.mutex.RLock()
	defer srv.mutex.RUnlock()
	return srv.sessions[sessionId]
}
