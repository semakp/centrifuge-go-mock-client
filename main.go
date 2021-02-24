// Private channel subscription example.
package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path"
	"sync"
	"time"

	"github.com/centrifugal/centrifuge-go"
	"gopkg.in/yaml.v2"
)

type Settings struct {
	LogFilename   string `yaml:"log-filename"`
	HTTPAddr      string `yaml:"http-addr"`
	CentrifugoUrl string `yaml:"centrifugo-url"`
}

var settings = Settings{}

type User struct {
	id string
	stop chan int
	isConnected bool
	isSubscribed bool
}

// Потокобезопасный map пользователей
type SafeUsers struct {
	mu sync.Mutex
	users map[string]User
}

type UsersCounters struct {
	total int
	connected int
	subscribed int
}

func (u *SafeUsers) SetConnected(userid string, state bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	user, present := u.users[userid]
	if present {
		user.isConnected = state
		u.users[userid] = user
	}
}

func (u *SafeUsers) SetSubscribed(userid string, state bool) {
	u.mu.Lock()
	defer u.mu.Unlock()

	user, present := u.users[userid]
	if present {
		user.isSubscribed = state
		u.users[userid] = user
	}
}

func (u *SafeUsers) GetCounters() UsersCounters {
	u.mu.Lock()
	defer u.mu.Unlock()

	var connected, subscribed int
	for _, user := range u.users {
		if user.isConnected {
			connected++
		}
		if user.isSubscribed {
			subscribed++
		}
	}
	return UsersCounters {len(u.users), connected, subscribed}
}

func (u *SafeUsers) GetUser(userid string) (User, bool) {
	u.mu.Lock()
	defer u.mu.Unlock()

	user, present := u.users[userid]
	return user, present
}

func (u *SafeUsers) AddUser(userid string, centrifugoUrl string, cookies []*http.Cookie) bool {
	u.mu.Lock()
	defer u.mu.Unlock()

	_, present := u.users[userid]
	if present {
		return false
	}

	stop := make(chan int)
	u.users[userid] = User {userid, stop, false, false }
	go newClient(userid, centrifugoUrl, stop, cookies)
	return true
}

func (u *SafeUsers) RemoveUser(userid string) bool {
	u.mu.Lock()
	defer u.mu.Unlock()

	user, present := u.users[userid]
	if !present {
		return false
	}

	user.stop <- 0
	delete(u.users, userid)
	return true
}

func (u *SafeUsers) RemoveUsers() {
	u.mu.Lock()
	defer u.mu.Unlock()

	for userid, user := range u.users {
		user.stop <- 0
		delete(u.users, userid)
	}
}

var safeUsers = SafeUsers{users: make(map[string]User)}

type eventHandler struct {
	userid string
}

func (h *eventHandler) OnConnect(_ *centrifuge.Client, _ centrifuge.ConnectEvent) {
	log.Printf("[User %s] Connected\n", h.userid)
	safeUsers.SetConnected(h.userid, true)
}

func (h *eventHandler) OnError(_ *centrifuge.Client, e centrifuge.ErrorEvent) {
	log.Printf("[User %s] Connection error message: %s\n", h.userid, e.Message)
	safeUsers.SetConnected(h.userid, false)
}

func (h *eventHandler) OnDisconnect(_ *centrifuge.Client, e centrifuge.DisconnectEvent) {
	log.Printf("[User %s] Disconnected reason: %s\n", h.userid, e.Reason)
	safeUsers.SetConnected(h.userid, false)
}

func (h *eventHandler) OnServerSubscribe(_ *centrifuge.Client, e centrifuge.ServerSubscribeEvent) {
	log.Printf("[User %s] Subscribe to server-side channel %s: (resubscribe: %t, recovered: %t)\n", h.userid, e.Channel, e.Resubscribed, e.Recovered)
}

func (h *eventHandler) OnServerUnsubscribe(_ *centrifuge.Client, e centrifuge.ServerUnsubscribeEvent) {
	log.Printf("[User %s] Unsubscribe from server-side channel %s\n", h.userid, e.Channel)
}

type subEventHandler struct {
	userid string
}

func (h *subEventHandler) OnSubscribeSuccess(sub *centrifuge.Subscription, _ centrifuge.SubscribeSuccessEvent) {
	log.Printf("[User %s] Subscribed to private channel %s\n", h.userid, sub.Channel())
	safeUsers.SetSubscribed(h.userid, true)
}

func (h *subEventHandler) OnSubscribeError(sub *centrifuge.Subscription, e centrifuge.SubscribeErrorEvent) {
	log.Printf("[User %s] Error subscribing to private channel %s: %v\n", h.userid, sub.Channel(), e.Error)
	safeUsers.SetSubscribed(h.userid, false)
}

func (h *subEventHandler) OnUnsubscribe(sub *centrifuge.Subscription, _ centrifuge.UnsubscribeEvent) {
	log.Printf("[User %s] Unsubscribed from private channel %s\n", h.userid, sub.Channel())
	safeUsers.SetSubscribed(h.userid, false)
}

func (h *subEventHandler) OnPublish(sub *centrifuge.Subscription, e centrifuge.PublishEvent) {
	log.Printf("[User %s] Received message from channel %s: %s\n", h.userid, sub.Channel(), string(e.Data))
}

func logUsersCounter() {
	for range time.Tick(time.Minute) {
		counters := safeUsers.GetCounters()
		log.Println("Total users ", counters.total, " connected ", counters.connected, " subscribed ", counters.subscribed)
	}
}

func newClient(userid string, centrifugoUrl string, stop chan int, cookies []*http.Cookie) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		log.Fatal(err)
	}

	cent, err := url.Parse(centrifugoUrl)
	if err != nil {
		log.Printf("Can't parse centrifugoUrl %s\n", centrifugoUrl)
		return
	}
	jar.SetCookies(cent, cookies)

	config := centrifuge.DefaultConfig()
	config.CookieJar = jar

	if cent.Scheme == "http" {
		cent.Scheme = "ws"
	} else {
		cent.Scheme = "wss"
	}

	cent.Path = path.Join(cent.Path, "/connection/websocket")
	client := centrifuge.New(cent.String(), config)
	defer func() {
		close(stop)
		_ = client.Close()
	}()

	handler := &eventHandler{userid}
	client.OnDisconnect(handler)
	client.OnConnect(handler)
	client.OnError(handler)

	err = client.Connect()
	if err != nil {
		log.Printf("[User %s] Connection with centrifugo error: %v\n", userid, err)
	}

	sub, err := client.NewSubscription(fmt.Sprintf("#%s", userid))
	if err != nil {
		log.Printf("[User %s] Subscription to centrifugo channel error: %v\n", userid, err)
	}

	subEventHandler := &subEventHandler{userid}
	sub.OnSubscribeSuccess(subEventHandler)
	sub.OnSubscribeError(subEventHandler)
	sub.OnUnsubscribe(subEventHandler)
	sub.OnPublish(subEventHandler)

	// Subscribe on private channel.
	_ = sub.Subscribe()

	for {
		select {
			case <- stop:
				fmt.Println("user exit ", userid)
				return
		}
	}
}

type RequestAdd struct {
	Id             string
	Many           int
	CentrifugoUrl  string
	Cookie         string
}

type RequestRemove struct {
	Id     string
}

type ResponseCount struct {
	Total      int
	Connected  int
	Subscribed int
}

func ApiServer(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path[1:] {
	case "connection.add":
		if r.Method != "POST" {
			w.WriteHeader(500)
			_, _ = fmt.Fprint(w, "Only POST is allowed")
			return
		}

		reader := r.Body
		body, err := ioutil.ReadAll(reader)
		if err != nil {
			w.WriteHeader(500)
			_, _ = fmt.Fprintf(w, "Can't read the body: %v", err)
			return
		}

		var requestAdd RequestAdd
		if err := json.Unmarshal(body, &requestAdd); err != nil {
			w.WriteHeader(500)
			_, _ = fmt.Fprintf(w, "Json body parse error: %v", err)
			return
		}

		fmt.Printf("requestAdd: %v\n", requestAdd)

		if requestAdd.Id == "" && requestAdd.Many == 0 {
			w.WriteHeader(500)
			_, _ = fmt.Fprintf(w, "User ID is not specified")
			return
		}
		var rawCentrifugoUrl = settings.CentrifugoUrl
		if requestAdd.CentrifugoUrl != "" {
			rawCentrifugoUrl = requestAdd.CentrifugoUrl
		}

		if rawCentrifugoUrl == "" {
			w.WriteHeader(500)
			_, _ = fmt.Fprintf(w, "Centrifugo Url is not specified")
			return
		}

		centrifugoUrl, err := url.Parse(rawCentrifugoUrl)
		if err != nil {
			w.WriteHeader(500)
			_, _ = fmt.Fprintf(w, "Can't parse centrifugoUrl %s", centrifugoUrl)
			return
		}

		if centrifugoUrl.Scheme == "ws" {
			centrifugoUrl.Scheme = "http"
		} else if centrifugoUrl.Scheme == "wss" {
			centrifugoUrl.Scheme = "https"
		}
		rawCentrifugoUrl = centrifugoUrl.String()

		var cookies = r.Cookies()
		if requestAdd.Cookie != "" {
			header := http.Header{}
			header.Add("Cookie", requestAdd.Cookie)
			request := http.Request{
				Header: header,
			}
			cookies = request.Cookies()
		}

		if len(cookies) == 0 {
			w.WriteHeader(500)
			_, _ = fmt.Fprintf(w, "Cookies not found")
			return
		}

		if requestAdd.Many > 0 {
			for i := 0; i < requestAdd.Many; i++ {
				userid := fmt.Sprintf("%d", i)
				var result string
				if safeUsers.AddUser(userid, rawCentrifugoUrl, cookies) {
					result = fmt.Sprintf("added to %s", rawCentrifugoUrl)
				} else {
					result = "already exists"
				}
				message := fmt.Sprintf("User %s is %s\n", userid, result)
				_, _ = fmt.Fprint(w, message)
				log.Print(message)
			}
			return
		}

		var result string
		if safeUsers.AddUser(requestAdd.Id, rawCentrifugoUrl, cookies) {
			result = fmt.Sprintf("added to %s", rawCentrifugoUrl)
		} else {
			result = "already exists"
		}
		message := fmt.Sprintf("User %s is %s\n", requestAdd.Id, result)
		_, _ = fmt.Fprint(w, message)
		log.Print(message)

	case "connection.remove":
		if r.Method != "POST" {
			w.WriteHeader(500)
			_, _ = fmt.Fprint(w, "Only POST is allowed")
			return
		}

		reader := r.Body
		body, err := ioutil.ReadAll(reader)
		if err != nil {
			w.WriteHeader(500)
			_, _ = fmt.Fprintf(w, "Can't read the body: %v", err)
			return
		}

		var requestRemove RequestRemove
		if err := json.Unmarshal(body, &requestRemove); err != nil {
			w.WriteHeader(500)
			_, _ = fmt.Fprintf(w, "Json body parse error: %v", err)
			return
		}

		if requestRemove.Id == "" {
			w.WriteHeader(500)
			_, _ = fmt.Fprintf(w, "User ID is not specified")
			return
		}

		var result string
		if safeUsers.RemoveUser(requestRemove.Id) {
			result = "removed"
		} else {
			result = "not exists"
		}
		message := fmt.Sprintf("User %s is %s\n", requestRemove.Id, result)
		_, _ = fmt.Fprint(w, message)
		log.Print(message)

	case "connection.clean":
		safeUsers.RemoveUsers()
		log.Print("Users clean")

	case "connection.count":
		counters := safeUsers.GetCounters()
		resp := ResponseCount{ Total: counters.total, Connected: counters.connected, Subscribed: counters.subscribed }
		jr, err := json.Marshal(resp)
		if err != nil {
			w.WriteHeader(500)
			_, _ = fmt.Fprintf(w, "Make json error: %v", err)
			return
		}
		_, _ = w.Write(jr)

	default:
		w.WriteHeader(404)
		_, _ = fmt.Fprint(w, "Not found")
	}
}

func serveHttp(addr string) {
	fmt.Println("Listening at ", addr)
	http.HandleFunc("/", ApiServer)
	err := http.ListenAndServe(addr, nil)
	if err != nil {
		log.Printf("HTTP server failed to start: %v", err)
	}
}

func readSettings(filename string, sets *Settings)  {
	data, err := ioutil.ReadFile(filename)
	if err != nil {
		log.Fatalf("error reading settings file: %v", err)
	}

	err = yaml.Unmarshal(data, &sets)
	if err != nil {
		log.Fatalf("error parsing settings file: %v", err)
	}
}

// Логгирование в файл
func startLoggingToFile(logFileName string) {
	if logFileName != "" {
		f, err := os.OpenFile(logFileName, os.O_RDWR | os.O_CREATE | os.O_APPEND, 0666)
		if err != nil {
			log.Fatalf("error opening log file: %v", err)
		}
		defer func() {
			_ = f.Close()
		}()
		log.SetOutput(f)

		fmt.Println("Start logging to file", logFileName)
		log.Println("=====Start logging=====")
	} else {
		fmt.Println("Start logging to console")
	}
}

func main() {
	readSettings("settings.yaml", &settings)
	startLoggingToFile(settings.LogFilename)

	log.Printf("Settings: %v\n", settings)

	go logUsersCounter()
	serveHttp(settings.HTTPAddr)
}