package main

// Notes:
// - the clientMap needs a mutex
// - the server socket probably needs a mutex
// - maybe the client sockets need mutexes?

import (
	"flag"
	"fmt"
	"github.com/mnakama/flexim-go/proto"
	"gopkg.in/yaml.v2"
	"io/ioutil"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// User config variables
var config struct {
	Nickname string
	Address  string
}

var (
	server        *proto.Socket
	clientMap     = make(map[string]*proto.Socket, 1)
	serverAddress = flag.String("server", "hive.nullcorp.org:8000", "fleximd server to connect to")
	username      = flag.String("user", "", "login name")
	tcplisten     = flag.String("tcplisten", "", "bind address for TCP clients")
	unixlisten    = flag.String("listen", "", "bind address for local clients")

	// X.org crashes at about 50+ visible windows with dwm
	chatLimit = flag.Int("chatlimit", 30, "flood protection: maximum amount of open chats")
)

func login() error {
	var err error

	server, err = proto.Dial("tcp", *serverAddress, proto.ModeMsgpack)
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
		fmt.Println(status)
		if status.Status < 0 {
			quit(1)
		}
	})

	auth := proto.Command{
		Cmd:     "AUTH",
		Payload: []string{*username},
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

	sock, err := proto.FromFD(fd[0], proto.ModeMsgpack)
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

	proc, err := os.StartProcess("/home/matt/projects/flexim-go/flexim-chat", []string{"flexim-go", "--fd", "3", "--mode", "msgpack", "--to", partner, "--user", *username}, &pattr)
	if err != nil {
		log.Print(err)
		return
	}

	log.Printf("Pid: %v", proc.Pid)

	clientMap[partner] = sock

	sock.SetCallbacks(func(msg *proto.Message) { //msg
		fmt.Println(msg)
		msg.From = *username
		server.SendMessage(msg)
	}, func(cmd *proto.Command) { // cmd
		fmt.Println(cmd)

	}, func(txt string) { // text
		fmt.Println(txt)

	}, func() { // disconnect
		fmt.Println("Chat window disconnected")
		delete(clientMap, partner)

	}, func(status *proto.Status) { // status
		fmt.Println(status)
	})
}

func newChatOut(conn net.Conn) {
	sock := proto.FromConn(conn, proto.ModeMsgpack)
	to := ""

	sock.SetCallbacks(func(msg *proto.Message) { //msg
		fmt.Println(msg)
		if to == "" && msg.To != "" {
			to = msg.To
			clientMap[to] = sock
		}
		msg.From = *username
		server.SendMessage(msg)
	}, func(cmd *proto.Command) { // cmd
		fmt.Println(cmd)

	}, func(txt string) { // text
		fmt.Println(txt)

	}, func() { // disconnect
		if to != "" {
			delete(clientMap, to)
		}

	}, func(status *proto.Status) { // status
		fmt.Println(status)
	})
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

	yconfig, err := ioutil.ReadFile("/home/matt/test.yaml")
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

	login()

	if *tcplisten != "" {
		ln, err := net.Listen("tcp", *tcplisten)
		if err != nil {
			log.Fatal(err)
		}
		go listenLoop(ln)
	}

	if *unixlisten == "" {
		*unixlisten = "/tmp/" + *serverAddress
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
