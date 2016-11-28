package engineio

import (
	"io"
	"net/http"
	"sync"
	"time"

	"gopkg.in/googollee/go-engine.io.v1/base"
	"gopkg.in/googollee/go-engine.io.v1/transport"
	"gopkg.in/googollee/go-engine.io.v1/transport/polling"
	"gopkg.in/googollee/go-engine.io.v1/transport/websocket"
)

func defaultChecker(*http.Request) (http.Header, error) {
	return nil, nil
}

// Config is server configure.
type Config struct {
	RequestChecker     func(*http.Request) (http.Header, error)
	PingTimeout        time.Duration
	PingInterval       time.Duration
	Transports         []transport.Transport
	SessionIDGenerator SessionIDGenerator
}

func (c *Config) fillNil() {
	if c.RequestChecker == nil {
		c.RequestChecker = defaultChecker
	}
	if c.PingTimeout == 0 {
		c.PingTimeout = time.Minute
	}
	if c.PingInterval == 0 {
		c.PingInterval = time.Second * 20
	}
	if len(c.Transports) == 0 {
		c.Transports = []transport.Transport{
			polling.Default,
			websocket.Default,
		}
	}
	if c.SessionIDGenerator == nil {
		c.SessionIDGenerator = &defaultIDGenerator{}
	}
}

// Server is server.
type Server struct {
	transports     *transport.Manager
	pingInterval   time.Duration
	pingTimeout    time.Duration
	sessions       *manager
	requestChecker func(*http.Request) (http.Header, error)
	locker         sync.RWMutex
	connChan       chan Conn
	closeOnce      sync.Once
}

// NewServer returns a server.
func NewServer(c *Config) (*Server, error) {
	if c == nil {
		c = &Config{}
	}
	conf := *c
	conf.fillNil()
	t := transport.NewManager(conf.Transports)
	return &Server{
		transports:     t,
		pingInterval:   conf.PingInterval,
		pingTimeout:    conf.PingTimeout,
		requestChecker: conf.RequestChecker,
		sessions:       newManager(c.SessionIDGenerator),
		connChan:       make(chan Conn, 1),
	}, nil
}

// Close closes server.
func (s *Server) Close() error {
	s.closeOnce.Do(func() {
		close(s.connChan)
	})
	return nil
}

// Accept accepts a connection.
func (s *Server) Accept() (Conn, error) {
	c := <-s.connChan
	if c == nil {
		return nil, io.EOF
	}
	return c, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	sid := query.Get("sid")
	session := s.sessions.Get(sid)
	t := query.Get("transport")
	tspt := s.transports.Get(t)

	if tspt == nil {
		http.Error(w, "invalid transport", http.StatusBadRequest)
		return
	}
	if session == nil {
		if sid != "" {
			http.Error(w, "invalid sid", http.StatusBadRequest)
			return
		}
		header, err := s.requestChecker(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		for k, v := range header {
			w.Header()[k] = v
		}
		conn, err := tspt.Accept(w, r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		params := base.ConnParameters{
			PingInterval: s.pingInterval,
			PingTimeout:  s.pingTimeout,
			Upgrades:     s.transports.UpgradeFrom(t),
		}
		session, err = newSession(s.sessions, t, conn, params)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}

		go func() {
			w, err := session.nextWriter(base.FrameString, base.OPEN)
			if err != nil {
				session.Close()
				return
			}
			if _, err := session.params.WriteTo(w); err != nil {
				w.Close()
				session.Close()
				return
			}
			if err := w.Close(); err != nil {
				session.Close()
				return
			}
			s.connChan <- session
		}()
	}
	if session.Transport() != t {
		header, err := s.requestChecker(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		for k, v := range header {
			w.Header()[k] = v
		}
		conn, err := tspt.Accept(w, r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		session.upgrade(t, conn)
		if handler, ok := conn.(http.Handler); ok {
			handler.ServeHTTP(w, r)
		}
		return
	}
	session.serveHTTP(w, r)
}
