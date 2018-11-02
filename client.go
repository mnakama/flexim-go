package main

// Notes:
// - the clientMap needs a mutex
// - the server socket probably needs a mutex
// - maybe the client sockets need mutexes?

import (
	"fmt"
	"github.com/mnakama/flexim-go/proto"
	"gopkg.in/yaml.v2"
	"io/ioutil"
	"log"
	"net"
)

// User config variables
var config struct {
	Nickname string
	Address  string
}

var (
	server      *proto.Socket
	clientMap = make(map[string]*proto.Socket, 1)
)

func login() {
	var err error

	server, err = proto.Dial("tcp", "hive.nullcorp.org:8000", proto.ModeMsgpack)
	if err != nil {
		log.Fatal(err)
	}

	err = server.SendHeader()
	if err != nil {
		log.Panic(err)
	}

	server.SetCallbacks(func(msg *proto.Message) {
		fmt.Printf("From: %s Msg: %s\n", msg.From, msg.Msg)

		client, exists := clientMap[msg.From]
		fmt.Printf("Client: %v Exists: %v\n", client, exists)
		if exists {
			client.SendMessage(msg)
		} else {
			fmt.Println("No chat window open for this conversation")
			// TODO: open chat client for incoming message
		}

	}, func(cmd *proto.Command) {
		fmt.Println(cmd)

	}, func(txt string) {
		fmt.Println(txt)
	})

	auth := proto.Command{
		Cmd:     "AUTH",
		Payload: []string{"gnuman"},
	}
	server.SendCommand(&auth)
}

func newChatOut(conn net.Conn) {
	sock := proto.FromConn(conn, proto.ModeMsgpack)
	to := ""

	sock.SetCallbacks(func(msg *proto.Message) {
		fmt.Println(msg)
		if to == "" && msg.To != "" {
			to = msg.To
			clientMap[to] = sock
		}
		server.SendMessage(msg)
	}, func(cmd *proto.Command) {
		fmt.Println(cmd)
	}, func(txt string) {
		fmt.Println(txt)
	})
}

func main() {
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

	login()

	ln, err := net.Listen("tcp", "localhost:8500")
	if err != nil {
		log.Fatal(err)
	}

	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Fatal(err)
		}

		newChatOut(conn)
	}
}
