package main

// Notes:
// - the clientMap needs a mutex
// - the server socket probably needs a mutex
// - maybe the client sockets need mutexes?

import (
	"encoding/hex"
	"flag"
	"fmt"
	"github.com/adrg/xdg"
	"github.com/mnakama/flexim-go/proto"
	"gopkg.in/yaml.v2"
	"io/ioutil"
	"log"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

// User config variables
var config struct {
	Nickname string
	Address  string
}

var (
	server        proto.Socket
	lastClient    *proto.Socket
	clientMap     = make(map[string]*proto.Socket, 1)
	serverAddress = flag.String("server", "hive.nullcorp.org:8000", "fleximd server to connect to")
	username      = flag.String("user", "", "login name")
	pubkey        string
	tcplisten     = flag.String("tcplisten", "", "bind address for TCP clients")
	unixlisten    = flag.String("listen", "", "bind address for local clients")

	// X.org crashes at about 50+ visible windows with dwm
	chatLimit = flag.Int("chatlimit", 30, "flood protection: maximum amount of open chats")
)

func login() error {
	var err error

	server.SetMode(proto.ModeMsgpack)

	err = server.Dial("tcp", *serverAddress)
	if err != nil {
		return err
	}

	err = server.SendHeader()
	if err != nil {
		log.Panic(err)
	}

	server.SetCallbacks(func(msg *proto.Message) { // msg
		fmt.Printf("From: %s Msg: %s\n", msg.From, msg.Msg)

		client, exists := clientMap[msg.From]
		fmt.Printf("Client: %v Exists: %v\n", client, exists)
		if exists {
			client.SendMessage(msg)
		} else {
			fmt.Println("No chat window open for this conversation")
			// TODO: open chat client for incoming message
			newChatIn(msg)
		}

	}, func(cmd *proto.Command) { // cmd
		fmt.Println(cmd)

	}, func(txt string) { // txt
		fmt.Println(txt)
	}, func() { // disconnect
		fmt.Println("Disconnected from server")
		go reconnect()
	}, func(status *proto.Status) { // status
		if lastClient != nil {
			lastClient.SendStatus(status)
		}
	}, func(roster *proto.Roster) { // roster
		if lastClient != nil {
			lastClient.SendRoster(roster)
		}
	}, func(auth *proto.Auth) { // auth
		fmt.Printf("Challenge received: %s\n", auth.Challenge)

		resp := proto.AuthResponse{Challenge: auth.Challenge}
		err := server.SendAuthResponse(&resp)
		if err != nil {
			log.Print(err)
		}
	})

	auth := proto.Command{
		Cmd:     "AUTH",
		Payload: []string{pubkey, *username},
	}
	server.SendCommand(&auth)

	return nil
}

func reconnect() {
	for {
		time.Sleep(time.Second)
		err := login()
		if err != nil {
			log.Println(err)
		} else {
			return
		}
	}
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

func newChatIn(msg *proto.Message) {
	if len(clientMap) >= *chatLimit {
		fmt.Printf("Too many open chats! (%d)\nMessage: %v\n\n", len(clientMap), *msg)
		return
	}

	partner := msg.From
	fd, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		log.Panic(err)
	}

	var sock proto.Socket
	sock.SetMode(proto.ModeMsgpack)

	err = sock.UseFD(fd[0])
	if err != nil {
		log.Panic(err)
	}

	err = sock.SendHeader()
	if err != nil {
		log.Panic(err)
	}
	err = sock.SendMessage(msg)
	if err != nil {
		log.Panic(err)
	}

	clientFile := os.NewFile(uintptr(fd[1]), "")
	if clientFile == nil {
		log.Panicln("Could not get file from file descriptor:", fd[1])
	}
	defer clientFile.Close()

	// exec
	pattr := os.ProcAttr{
		Files: []*os.File{nil, os.Stdout, os.Stderr, clientFile},
	}

	proc, err := os.StartProcess("flexim-chat", []string{"flexim-chat", "--fd", "3", "--mode", "msgpack", "--to", partner, "--user", *username}, &pattr)
	if err != nil {
		log.Print(err)
		return
	}

	log.Printf("Pid: %v", proc.Pid)

	clientMap[partner] = &sock

	sock.SetCallbacks(func(msg *proto.Message) { //msg
		log.Printf("client -> server: %+v\n", msg)
		// override From with pubkey
		msg.From = pubkey
		server.SendMessage(msg)

		lastClient = &sock
	}, func(cmd *proto.Command) { // cmd
		log.Println(cmd)

		server.SendCommand(cmd)
		lastClient = &sock
	}, func(txt string) { // text
		log.Println(txt)

	}, func() { // disconnect
		log.Println("Chat window disconnected")
		delete(clientMap, partner)

	},
		cb_Status, // status
		cb_Roster, // roster
		cb_Auth)   // auth
}

func newChatOut(conn net.Conn) {
	sock := proto.FromConn(conn, proto.ModeMsgpack)
	to := ""

	sock.SetCallbacks(func(msg *proto.Message) { //msg
		log.Printf("client -> server: %+v\n", msg)
		if to == "" && msg.To != "" {
			to = msg.To
			clientMap[to] = sock
		}

		// override From with pubkey
		msg.From = pubkey
		server.SendMessage(msg)

		lastClient = sock
	}, func(cmd *proto.Command) { // cmd
		log.Println(cmd)
		server.SendCommand(cmd)

		lastClient = sock
	}, func(txt string) { // text
		log.Println(txt)

	}, func() { // disconnect
		log.Println("Chat window disconnected")
		if to != "" {
			delete(clientMap, to)
		}

	},
		cb_Status, // status
		cb_Roster, // roster
		cb_Auth)   // auth
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

	for _, sock := range clientMap {
		sock.Close()
	}

	os.Exit(ret)
}

func main() {
	flag.Parse()

	yconfig, err := ioutil.ReadFile(xdg.ConfigHome + "/flexim/chat.yaml")
	if err != nil {
		log.Print(err)
	} else {
		err = yaml.Unmarshal(yconfig, &config)
		if err != nil {
			log.Print(err)
		}
	}

	fmt.Println(config)

	if *username == "" {
		*username = config.Nickname
	}

	// placeholder: replace with actual pubkey when implemented
	pubkey = hex.EncodeToString([]byte(*username))

	login()

	if *tcplisten != "" {
		ln, err := net.Listen("tcp", *tcplisten)
		if err != nil {
			log.Fatal(err)
		}
		go listenLoop(ln)
	}

	// listen to a unix socket for clients
	if *unixlisten == "" {
		var err error
		*unixlisten, err = xdg.RuntimeFile("flexim/" + strings.ReplaceAll(*serverAddress, "/", "_"))
		if err != nil {
			log.Fatal(err)
		}
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
}
