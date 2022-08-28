package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/gorilla/websocket"
	"github.com/mnakama/flexim-go/proto"
	"gopkg.in/guregu/null.v4"
	"gopkg.in/yaml.v2"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"time"
)

type SnowflakeID string

type Author struct {
	ID               SnowflakeID `json:"id"`
	Username         string      `json:"username"`
	Avatar           string      `json:"avatar"`
	AvatarDecoration string      `json:"avatar_decoration"`
	Discriminator    string      `json:"discriminator"`
	PublicFlags      int         `json:"public_flags"`
}

type Attachment struct {
	ID          SnowflakeID `json:"id"`
	Filename    string      `json:"filename"`
	Size        int         `json:"size"`
	URL         string      `json:"url"`
	ProxyURL    string      `json:"proxy_url"`
	Width       int         `json:"width"`
	Height      int         `json:"height"`
	ContentType string      `json:"content_type"`
}

type Provider struct {
	Name string `json:"name"`
}

type Thumbnail struct {
	URL      string `json:"url"`
	ProxyURL string `json:"proxy_url"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
}

type Embed struct {
	Type        string    `json:"type"`
	URL         string    `json:"url"`
	Title       string    `json:"title,omitempty"`
	Description string    `json:"description,omitempty"`
	Color       int       `json:"color,omitempty"`
	Provider    Provider  `json:"provider,omitempty"`
	Thumbnail   Thumbnail `json:"thumbnail"`
}

type Mention interface{}

type Emoji struct {
	ID   interface{} `json:"id"`
	Name string      `json:"name"`
}

type Reaction struct {
	Emoji Emoji `json:"emoji"`
	Count int   `json:"count"`
	Me    bool  `json:"me"`
}

type MessageReference struct {
	ChannelID SnowflakeID `json:"channel_id"`
	GuildID   SnowflakeID `json:"guild_id"`
	MessageID SnowflakeID `json:"message_id"`
}

type MessageSend struct {
	Content string `json:"content"`
	Nonce   string `json:"nonce,omitempty"`
	TTS     bool   `json:"tts"`
}

type Message struct {
	ID              SnowflakeID   `json:"id,omitempty"`
	Type            int           `json:"type"`
	Content         string        `json:"content"`
	ChannelID       SnowflakeID   `json:"channel_id`
	Author          Author        `json:"author"`
	Attachments     []Attachment  `json:"attachments"`
	Embeds          []Embed       `json:"embeds"`
	Mentions        []Mention     `json:"mentions"`
	MentionRoles    []interface{} `json:"mention_roles"`
	Pinned          bool          `json:"pinned"`
	MentionEveryone bool          `json:"mention_everyone"`
	TTS             bool          `json:"tts"`
	Timestamp       time.Time     `json:"timestamp"`
	EditedTimestamp null.Time     `json:"edited_timestamp"`
	Flags           int           `json:"flags"`
	Components      []interface{} `json:"components"`

	Nonce             SnowflakeID `json:"nonce,omitempty"`
	MessageReference  interface{} `json:"message_reference,omitempty"`
	ReferencedMessage *Message    `json:"referenced_message,omitempty"`
	Reactions         []Reaction  `json:"reactions,omitempty"`
}

// User config variables
var config struct {
	AuthToken string
	Nicknames map[string]string
}

var (
	lastClient *proto.Socket
	clientMap  = make(map[string]*proto.Socket, 1)
	tcplisten  = flag.String("tcplisten", "", "bind address for TCP clients")
	unixlisten = flag.String("listen", "", "bind address for local clients")
	configFile = flag.String("c", os.ExpandEnv("$HOME/.config/flexim/discord.yaml"), "config file")

	// X.org crashes at about 50+ visible windows with dwm
	chatLimit = flag.Int("chatlimit", 30, "flood protection: maximum amount of open chats")

	client = http.Client{}

	self = Author{
		ID:            SnowflakeID("311322749010182145"),
		Username:      "gnuman",
		Avatar:        "29483b1432ba08fe2ed3b24a4b5964ad",
		Discriminator: "4946",
	}
)

// Send a desktop notification.
//
// Hopefully I can connect to dbus properly in the future, but for now, we just call out to CLI
func notify(channel, text string) {
	c := exec.Command("notify-send", "-t", "30000", channel, text)
	c.Start()
	go c.Wait()
}

func main() {
	flag.Parse()

	yconfig, err := os.ReadFile(*configFile)
	if err != nil {
		log.Fatal(err)
	}

	yaml.Unmarshal(yconfig, &config)
	if err != nil {
		log.Fatal(err)
	}

	log.Printf("%+v", config)

	// maybe listen to a tcp socket for clients
	if *tcplisten != "" {
		ln, err := net.Listen("tcp", *tcplisten)
		if err != nil {
			log.Fatal(err)
		}
		go listenLoop(ln)
	}

	// listen to a unix socket for clients
	if *unixlisten == "" {
		*unixlisten = "/tmp/flexim-discord"
	}

	os.Remove(*unixlisten)
	ln, err := net.Listen("unix", *unixlisten)
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		r := recover()
		if r != nil {
			fmt.Println("Panic:", r)
		}

		quit(1)
	}()

	go listenLoop(ln)

	waitSignal()

	messageJSON, err := os.Open("/tmp/msg.json")
	if err != nil {
		log.Fatal(err)
	}

	var messages []Message = make([]Message, 0, 50)

	decoder := json.NewDecoder(messageJSON)
	decoder.Decode(&messages)

	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		fmt.Printf("%s#%s: %s\n", msg.Author.Username, msg.Author.Discriminator, msg.Content)

		for _, a := range msg.Attachments {
			fmt.Printf("Name: %s\n", a.Filename)
			fmt.Printf("Type: %s\n", a.ContentType)
			fmt.Printf("URL: %s\n", a.URL)
		}

		for _, e := range msg.Embeds {
			if e.Title != "" {
				fmt.Printf("Title: %s\n", e.Title)
			}
			if e.Description != "" {
				fmt.Printf("Description: %s\n", e.Description)
			}
			/*if e.URL != "" && e.URL != msg.Content {
				fmt.Printf("URL: %s\n", e.URL)
			}*/
			/*if e.Thumbnail.ProxyURL != "" {
				fmt.Printf("Proxy URL: %s\n", e.Thumbnail.ProxyURL)
			}*/
		}

		if len(msg.Reactions) > 0 {
			for _, r := range msg.Reactions {
				fmt.Printf("%s%d ", r.Emoji.Name, r.Count)
			}
			fmt.Println()
		}

		fmt.Println()
	}

	notify("done", "done")
}

func connectToDiscord() {
	const discordWebsocketURL = "gateway.discord.gg/"

	u := url.URL{
		Scheme:   "wss",
		Host:     discordWebsocketURL,
		Path:     "/echo",
		RawQuery: "encoding=json&v=9&compress=zlib-stream",
	}
	c, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		err := c.Close()
		if err {
			log.Print(err)
		}
	}()

	for {
		_, message, err := c.ReadMessage()
		if err != nil {
			log.Printf("error reading websocket message: %w", err)
		}

		log.Printf("recv: %s", message)
	}
}

func listenLoop(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Fatal(err)
		}

		newChatOut(conn)
	}
}

func newChatOut(conn net.Conn) {
	sock := proto.FromConn(conn, proto.ModeMsgpack)

	setCallbacks(sock, "")
}

func setCallbacks(sock *proto.Socket, clientID string) {
	const maxIRCLen = 510

	sock.SetCallbacks(func(msg *proto.Message) { //msg
		log.Printf("client -> server: %+v\n", msg)
		if clientID == "" && msg.To != "" {
			clientID = strings.ToLower(msg.To)
			clientMap[clientID] = sock
		}

		SendMessage(msg)

		lastClient = sock
	}, func(cmd *proto.Command) { // cmd
		log.Println(cmd)

		lastClient = sock

	}, func(txt string) { // text
		log.Println(txt)

	}, func() { // disconnect
		log.Println("Chat window disconnected")
		if clientID != "" {
			/*if strings.HasPrefix(clientID, "#") {
				fmt.Fprintf(irc, "PART %s\n", clientID)
			}*/
			delete(clientMap, clientID)
		}

	},
		cb_Status, // status
		cb_Roster, // roster
		cb_Auth)   // auth
}

func SendMessage(pmsg *proto.Message) {
	msg := MessageSend{
		Content: pmsg.Msg,
	}

	data, err := json.Marshal(&msg)
	if err != nil {
		log.Printf("error while encoding message to json: %w", err)
		return
	}

	channelID, found := config.Nicknames[pmsg.To]
	if !found {
		channelID = pmsg.To
	}

	fmt.Printf("To: %s ChannelID: %s\n", pmsg.To, channelID)
	fmt.Printf("data: %s\n\n", string(data))

	reader := bytes.NewReader(data)
	req, err := http.NewRequest(
		http.MethodPost,
		"https://discord.com/api/v9/channels/"+channelID+"/messages",
		reader)
	if err != nil {
		log.Print(err)
		return
	}

	req.Header.Add("Authorization", config.AuthToken)
	req.Header.Add("Content-Type", "application/json")

	res, err := client.Do(req)
	if err != nil {
		log.Print(err)
		return
	}

	body, err := io.ReadAll(res.Body)
	if err != nil {
		log.Printf("failed to read response body: %w", err)
	}

	if res.StatusCode != http.StatusOK {
		log.Printf("failed to send message (%d): %s", res.StatusCode, body)
		return
	}

	log.Printf("send message received response: %s", body)
}

func cb_Status(status *proto.Status) {
	log.Println(status)
}

func cb_Roster(roster *proto.Roster) {
	log.Println(roster)
}

func cb_Auth(auth *proto.Auth) {
	log.Println(auth)
}

// Catch interrupt signal
func waitSignal() {
	c := make(chan os.Signal)

	signal.Notify(c, os.Interrupt)
	s := <-c
	log.Println("Received signal:", s)

	quit(0)
}

// do cleanup
func quit(ret int) {
	fmt.Println("Closing down...")
	os.Remove(*unixlisten)

	cmd := proto.Command{
		Cmd: "BYE ",
	}
	for _, sock := range clientMap {
		sock.SendCommand(&cmd)
		sock.Close()
	}

	os.Exit(ret)
}
