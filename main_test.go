package main

import (
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"testing"
	"time"

	"github.com/garyburd/redigo/redis"
	"github.com/gorilla/websocket"
	. "github.com/smartystreets/goconvey/convey"
	"log"
	"bytes"
)

type Server struct {
	*httptest.Server
	URL string
}

type cstHandler struct {
	*testing.T
	app *Application
}

func (t cstHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	t.app.ServeHTTP(w, r)
}

func sendRecv(t *testing.T, ws *websocket.Conn) {
	const message = "ping"
	So(ws.SetWriteDeadline(time.Now().Add(time.Second)), ShouldBeNil)
	So(ws.WriteMessage(websocket.TextMessage, []byte(message)), ShouldBeNil)
	So(ws.SetReadDeadline(time.Now().Add(time.Second)), ShouldBeNil)
	_, p, err := ws.ReadMessage()
	So(err, ShouldBeNil)
	So(string(p), ShouldEqual, message)
}

func TestSecureTokens(t *testing.T) {
	Convey("Secure tokens", t, func() {
		secret = bytes.NewBufferString("secret").Bytes()
		u, err := url.Parse("ws://ws.tera-online.ru/ws/secure-user-1")
		ts := time.Unix(1441634853, 0)
		So(err, ShouldBeNil)
		q := u.Query()
		q.Add("token", "74d96668e05a0b0ea1f71009a5898f2c87ea3faf2c98bf91f02431fcb86a47d9")
		q.Add("ts", "1441634853")
		u.RawQuery = q.Encode()
		log.Println(u)
		So(tokenCorrect("secure-user-1", u, ts.Add(time.Second)), ShouldBeTrue)
	})
}

func TestDialConvey(t *testing.T) {
	Convey("Test", t, func() {
		a := New()
		s := newWSServer(t, a)
		defer s.Close()

		ws, _, err := cstDialer.Dial(s.URL, nil)
		So(err, ShouldBeNil)
		defer ws.Close()
		sendRecv(t, ws)
	})
}

func newServer(t *testing.T, a *Application) *Server {
	var s Server
	s.Server = httptest.NewServer(cstHandler{t, a})
	s.URL = s.Server.URL
	return &s
}

func newWSServer(t *testing.T, a *Application) *Server {
	s := newServer(t, a)
	s.URL = "ws" + s.Server.URL[len("http"):]
	return s
}

var cstUpgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	Error: func(w http.ResponseWriter, r *http.Request, status int, reason error) {
		http.Error(w, reason.Error(), status)
	},
}

var cstDialer = websocket.Dialer{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
}

func init() {
	port = 9288
	redisPrefix = "ws-test"
}

func TestApplication(t *testing.T) {
	Convey("Create", t, func() {
		app := getApplication()
		So(app, ShouldNotBeNil)
		Convey("Get facility", func() {
			f := app.Facility("launcher")
			So(f, ShouldNotBeNil)
		})
		Convey("Get facility from url", func() {
			u, _ := url.Parse("/ws/test?kek")
			f, _ := app.FacilityFromURL(u)
			So(f, ShouldNotBeNil)
			So(f.name, ShouldEqual, "test")
		})
		Convey("Websocket connection", func() {
			So(app.clients(), ShouldEqual, 0)
			So(app.requestsPerSecond(), ShouldEqual, 0)
			s := newWSServer(t, app)
			defer s.Close()
			ws, _, err := cstDialer.Dial(s.URL+"/facility?test=true", nil)
			So(err, ShouldBeNil)
			defer ws.Close()
			ws.SetReadDeadline(time.Now().Add(timeout))
			conn, err := redis.Dial(redisNetwork, redisAddr)
			So(err, ShouldBeNil)
			message := []byte("testmessage")
			_, err = conn.Do("select", redisDatabase)
			So(err, ShouldBeNil)
			So(currentVersion(), ShouldEqual, version)
			So(goroutines(), ShouldNotEqual, 0)
			Convey("Send from server", func() {
				_, err := conn.Do("publish", "ws-test:broadcast:facility", message)
				So(err, ShouldBeNil)
				Convey("Recieve on client", func() {
					t, readMessage, err := ws.ReadMessage()
					So(err, ShouldBeNil)
					So(t, ShouldEqual, websocket.TextMessage)
					So(reflect.DeepEqual(message, readMessage), ShouldBeTrue)
				})
			})
			Convey("Send non-message", func() {
				ws.SetReadDeadline(time.Now().Add(time.Millisecond * 10))
				So(ws.WriteMessage(websocket.BinaryMessage, []byte("123213")), ShouldBeNil)
				_, _, err := ws.ReadMessage()
				So(err, ShouldNotBeNil)
			})
		})
	})
}

func TestApplicationViaGet(t *testing.T) {
	strictMode = true
	Convey("Create", t, func() {
		app := getApplication()
		// So(app, ShouldNotBeNil)
		Convey("Get ok facility", func() {
			u, _ := url.Parse("/ws/launcher?asdsadas")
			f, err := app.FacilityFromURL(u)
			So(err, ShouldBeNil)
			So(f, ShouldNotBeNil)
			So(f.name, ShouldEqual, "launcher")
		})
		Convey("Get forbidden", func() {
			u, _ := url.Parse("/ws/forbidden?test")
			f, err := app.FacilityFromURL(u)
			So(err, ShouldBeNil)
			So(f, ShouldNotBeNil)
			// strict mode deprecation
			So(f.name, ShouldEqual, "forbidden")
		})
		Convey("Stat handler", func() {
			s := newServer(t, app)
			defer s.Close()
			res, err := http.Get(s.URL + "/stat")
			So(err, ShouldBeNil)
			So(res.StatusCode, ShouldEqual, http.StatusOK)
			b, err := ioutil.ReadAll(res.Body)
			So(err, ShouldBeNil)
			So(string(b), ShouldContainSubstring, version)
			So(string(b), ShouldContainSubstring, "ws4redis")
		})
	})
}
