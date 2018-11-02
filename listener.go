package main

import (
	"log"
	"net"
	"os"
)

const (
	modeText = "text"
	modeMsgpack = "msgpack"
)

func handoverConn(conn *net.TCPConn) {
	defer conn.Close()
	log.Printf("Connection: %v", conn)

	// Read header
	header := make([]byte, 5)
	length, err := conn.Read(header)
	if err != nil {
		log.Print(err)
		return
	}
	if length < 5 {
		log.Printf("Initial packet too short: %d, %s", length, header)
		return
	}

	// Check header
	var proto string
	switch string(header) {
	case "\x00FLEX":
		proto = modeText
	case "\xa4FLEX":
		proto = modeMsgpack
	default:
		log.Printf("Invalid connection header: %s", header)
		return
	}

	// Get File from Connection
	file, err := conn.File()
	if err != nil {
		log.Print(err)
		return
	}
	defer file.Close()
	log.Printf("File: %v, %v", file, file.Fd())

	// exec
	pattr := os.ProcAttr{
		Files: []*os.File{nil, os.Stdout, os.Stderr, file},
	}

	proc, err := os.StartProcess("/home/matt/projects/flexim-go/flexim-chat", []string{"flexim-go", "--fd", proto}, &pattr)
	if err != nil {
		log.Print(err)
		return
	}

	log.Printf("Pid: %v", proc.Pid)
}

func main() {
	addr,err := net.ResolveTCPAddr("tcp", ":9001")
	if err != nil {
		log.Fatal(err)
	}

	ln, err := net.ListenTCP("tcp", addr)
	if err != nil {
		log.Fatal(err)
	}

	for {
		conn, err := ln.AcceptTCP()
		if err != nil {
			log.Fatal(err)
		}

		go handoverConn(conn)
	}
}
