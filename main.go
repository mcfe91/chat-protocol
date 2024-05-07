package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"log"
	"net"
	"strconv"
)

// TODO: splt into packages, add tests, mutex when creating channels, etc..., close connections gracefully with quitch, finish implementing remaining commands

var DELIMITER = []byte(`\r\n`)

type ID int

const (
	REG ID = iota
	JOIN
	LEAVE
	MSG
	CHNS
	USRS
)

type Command struct {
	id        ID
	recipient string
	sender    string
	body      []byte
}

type Client struct {
	conn       net.Conn
	outbound   chan Command
	register   chan *Client
	unregister chan *Client
	username   string
}

func newClient(conn net.Conn, outboundch chan Command, registerch chan *Client, unregisterch chan *Client) *Client {
	return &Client{
		conn:       conn,
		outbound:   outboundch,
		register:   registerch,
		unregister: unregisterch,
	}
}

func (c *Client) reg(args []byte) error {
	u := bytes.TrimSpace(args)
	if u[0] != '@' {
		return fmt.Errorf("username must begin with @")
	}
	if len(u) == 0 {
		return fmt.Errorf("username cannot be blank")
	}

	c.username = string(u)
	c.register <- c

	return nil
}

func (c *Client) msg(args []byte) error {
	args = bytes.TrimSpace(args)
	if args[0] != '#' && args[0] != '@' {
		return fmt.Errorf("recipient must be a channel ('#name') or ('@user')")
	}
	recipient := bytes.Split(args, []byte(" "))[0]
	if len(recipient) == 0 {
		return fmt.Errorf("recipient must have a name")
	}

	args = bytes.TrimSpace(bytes.TrimPrefix(args, recipient))
	l := bytes.Split(args, DELIMITER)[0]
	length, err := strconv.Atoi(string(l))
	if err != nil {
		return fmt.Errorf("body length must be present")
	}
	if length == 0 {
		return fmt.Errorf("body length must be at least 1")
	}

	padding := len(l) + len(DELIMITER)
	body := args[padding : padding+length]

	c.outbound <- Command{
		recipient: string(recipient),
		sender:    c.username,
		body:      body,
		id:        MSG,
	}

	return nil
}

func (c *Client) err(e error) {
	c.conn.Write([]byte("ERR " + e.Error() + "\n"))
}

func (c *Client) handle(message []byte) {
	cmd := bytes.ToUpper(bytes.TrimSpace(bytes.Split(message, []byte(" "))[0]))
	args := bytes.TrimSpace(bytes.TrimPrefix(message, cmd))

	switch string(cmd) {
	case "REG":
		if err := c.reg(args); err != nil {
			c.err(err)
		}
	// case "JOIN":
	// 	if err := c.join(args); err != nil {
	// 		c.err(err)
	// 	}
	// case "LEAVE":
	// 	if err := c.leave(args); err != nil {
	// 		c.err(err)
	// 	}
	case "MSG":
		if err := c.msg(args); err != nil {
			c.err(err)
		}
	// case "CHNS":
	// 	c.chns()
	// case "USRS":
	// 	c.usrs()
	default:
		c.err(fmt.Errorf("unknown command %s", cmd))
	}
}

func (c *Client) readLoop() error {
	for {
		msg, err := bufio.NewReader(c.conn).ReadBytes('\n')
		if err == io.EOF {
			c.unregister <- c
			return nil
		}
		if err != nil {
			return err
		}
		c.handle(msg)
	}
}

type Channel struct {
	name    string
	clients map[*Client]bool
}

func newChannel(name string) *Channel {
	return &Channel{
		name: name,
	}
}

type Hub struct {
	channels        map[string]*Channel
	clients         map[string]*Client
	commands        chan Command
	deregistrations chan *Client
	registrations   chan *Client
}

func newHub() *Hub {
	return &Hub{
		channels:        make(map[string]*Channel),
		clients:         make(map[string]*Client),
		commands:        make(chan Command),
		deregistrations: make(chan *Client),
		registrations:   make(chan *Client),
	}
}

func (h *Hub) run() {
	for {
		select {
		case client := <-h.registrations:
			h.register(client)
		case client := <-h.deregistrations:
			h.unregister(client)
		case cmd := <-h.commands:
			switch cmd.id {
			case JOIN:
				h.joinChannel(cmd.sender, cmd.recipient)
			case LEAVE:
				// h.leaveChannel(cmd.sender, cmd.recipient)
			case MSG:
				h.message(cmd.sender, cmd.recipient, cmd.body)
			// case USRS:
			// 	h.listUsers(cmd.sender)
			// case CHNS:
			// 	h.listChannels(cmd.sender)
			default:
				// AHHHH!!!!
			}
		}
	}
}

func (h *Hub) register(c *Client) {
	if _, exists := h.clients[c.username]; exists {
		c.username = ""
		c.conn.Write([]byte("ERR username taken\n"))
	} else {
		h.clients[c.username] = c
		c.conn.Write([]byte("OK\n"))
	}
}

func (h *Hub) unregister(c *Client) {
	if _, exists := h.clients[c.username]; exists {
		delete(h.clients, c.username)

		for _, channel := range h.channels {
			delete(channel.clients, c)
		}
	}
}

func (h *Hub) joinChannel(u string, c string) {
	if client, ok := h.clients[u]; ok {
		if channel, ok := h.channels[c]; ok {
			channel.clients[client] = true
		}
	} else {
		h.channels[c] = newChannel(c)
		h.channels[c].clients[client] = true
	}
}

func (h *Hub) message(u string, r string, m []byte) {
	if sender, ok := h.clients[u]; ok {
		switch r[0] {
		case '#':
			if channel, ok := h.channels[r]; ok {
				if _, ok := channel.clients[sender]; ok {
					channel.broadcast(sender.username, m)
				}
			}
		case '@':
			if user, ok := h.clients[r]; ok {
				user.conn.Write(append(m, '\n'))
			} else {
				sender.conn.Write([]byte("ERR no such user"))
			}
		}
	}
}

func (c *Channel) broadcast(s string, m []byte) {
	msg := append([]byte(s), ": "...)
	msg = append(msg, m...)
	msg = append(msg, '\n')

	for cl := range c.clients {
		cl.conn.Write(msg)
	}
}

func main() {
	ln, err := net.Listen("tcp", ":3000")
	if err != nil {
		log.Fatal(err)
	}

	hub := newHub()
	go hub.run()

	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Printf("%v\n", err)
		}

		c := newClient(
			conn,
			hub.commands,
			hub.registrations,
			hub.deregistrations,
		)

		go c.readLoop()
	}
}
