package main

import (
	"crypto/tls"
	"encoding/json"
	"io/ioutil"
	"log"
	"os"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
)

type (
	serverConfig struct {
		Server   string `json:"server,omitempty"`
		Username string `json:"username,omitempty"`
		Password string `json:"password,omitempty"`
	}

	config struct {
		From    serverConfig      `json:"from,omitempty"`
		To      serverConfig      `json:"to,omitempty"`
		Mapping map[string]string `json:"mapping,omitempty"`
		Exclude []string          `json:"exclude,omitempty"`
	}
)

func createClient(cfg serverConfig) (*client.Client, error) {
	c, err := client.DialTLS(cfg.Server, &tls.Config{})
	if err != nil {
		return nil, err
	}
	err = c.Login(cfg.Username, cfg.Password)
	return c, err
}

func copyMailbox(from *client.Client, to *client.Client, source, dest string) error {
	mbox, err := from.Select(source, false)
	if err != nil {
		return err
	}
	if mbox.Messages == 0 {
		return nil
	}

	if err := to.Create(dest); err != nil && err.Error() != "Mailbox already exists." {
		return err
	}

	log.Printf("Copying %d messages", mbox.Messages)

	seqset := new(imap.SeqSet)
	seqset.AddRange(1, mbox.Messages)

	section := &imap.BodySectionName{}
	items := []imap.FetchItem{section.FetchItem(), imap.FetchFlags, imap.FetchInternalDate, imap.FetchRFC822Size, imap.FetchEnvelope}

	messages := make(chan *imap.Message, 10)
	done := make(chan error, 1)
	go func(messages chan *imap.Message, done chan error) {
		done <- from.Fetch(seqset, items, messages)
	}(messages, done)

	for msg := range messages {
		r := msg.GetBody(section)
		if err := to.Append(dest, msg.Flags, msg.InternalDate, r); err != nil {
			return err
		}
	}

	if err := <-done; err != nil {
		return err
	}
	return nil
}

func main() {
	configFilename := os.Getenv("CONFIG_FILE")
	configFile, err := ioutil.ReadFile(configFilename)
	if err != nil {
		log.Fatal(err)
	}

	cfg := config{}
	if err := json.Unmarshal(configFile, &cfg); err != nil {
		log.Fatal(err)
	}

	from, err := createClient(cfg.From)
	if err != nil {
		log.Fatal(err)
	}
	defer from.Close()

	to, err := createClient(cfg.To)
	if err != nil {
		log.Fatal(err)
	}
	defer to.Close()

	mailboxes := make(map[string]string)
	mboxSrv := make(chan *imap.MailboxInfo, 10)
	done := make(chan error, 1)
	go func() {
		done <- from.List("", "*", mboxSrv)
	}()
	for m := range mboxSrv {
		mailboxes[m.Name] = m.Name
		if override, ok := cfg.Mapping[m.Name]; ok {
			mailboxes[m.Name] = override
		}
	}
	if err := <-done; err != nil {
		log.Fatal(err)
	}

	if len(cfg.Exclude) != 0 {
		for _, v := range cfg.Exclude {
			delete(mailboxes, v)
		}
	}

	log.Println("Copying mailboxes:")
	for k, v := range mailboxes {
		log.Printf("%s -> %s\n", k, v)
	}

	for k, v := range mailboxes {
		log.Printf("Processing %s ... ", k)
		if err := copyMailbox(from, to, k, v); err != nil {
			log.Fatal(err)
		}
		log.Printf("Processing %s done ", k)
	}
}
