package websocket

import (
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/googollee/go-engine.io/base"
	"github.com/stretchr/testify/assert"
)

func TestWebsocketSetReadDeadline(t *testing.T) {
	at := assert.New(t)

	tran := &Transport{}
	conn := make(chan base.Conn, 1)
	handler := func(w http.ResponseWriter, r *http.Request) {
		c, err := tran.Accept(w, r)
		at.Nil(err)
		conn <- c
		c.(http.Handler).ServeHTTP(w, r)
	}
	httpSvr := httptest.NewServer(http.HandlerFunc(handler))
	defer httpSvr.Close()

	u, err := url.Parse(httpSvr.URL)
	at.Nil(err)
	u.Scheme = "ws"

	header := make(http.Header)
	cc, err := tran.Dial(u.String(), header)
	at.Nil(err)
	defer cc.Close()

	sc := <-conn
	defer sc.Close()

	cc.SetReadDeadline(time.Now().Add(time.Second / 10))
	start := time.Now()
	_, _, _, err = cc.NextReader()
	timeout, ok := err.(net.Error)
	at.True(ok)
	at.True(timeout.Timeout())
	end := time.Now()
	at.True(end.Sub(start) > time.Second/10)
}
