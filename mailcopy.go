package main

import (
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path"
	"sort"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
	"github.com/gosuri/uiprogress"
	"github.com/gosuri/uiprogress/util/strutil"
)

var (
	listOnly bool
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
		Include []string          `json:"include,omitempty"`
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

	total := mbox.Messages

	// TODO: Possible issue when messages is above int limit
	bar := uiprogress.AddBar(int(total)).AppendCompleted().PrependElapsed()
	bar.PrependFunc(func(b *uiprogress.Bar) string {
		return strutil.Resize(fmt.Sprintf("%s: %d/%d", source, b.Current(), total), 22)
	})

	if err := to.Create(dest); err != nil && err.Error() != "Mailbox already exists." {
		return err
	}
	for mbox.Messages > 0 {
		batch := uint32(10)
		if mbox.Messages < batch {
			batch = mbox.Messages
		}

		seqset := new(imap.SeqSet)
		seqset.AddRange(1, batch)

		section := &imap.BodySectionName{}
		items := []imap.FetchItem{section.FetchItem(), imap.FetchFlags, imap.FetchInternalDate, imap.FetchRFC822Size, imap.FetchEnvelope, imap.FetchUid}

		messages := make(chan *imap.Message, 10)
		done := make(chan error, 1)
		go func(messages chan *imap.Message, done chan error) {
			if err := from.Fetch(seqset, items, messages); err != nil {
				log.Printf("error occurred while fetching messages: %s", err)
				done <- err
			}
			close(done)
		}(messages, done)

		msgs := make([]uint32, 0)

	outer:
		for {
			select {
			case err := <-done:
				if err != nil {
					return err
				}
				break outer
			case msg, ok := <-messages:
				if !ok {
					// Channel closed, all mails received
					break outer
				}
				bar.Incr()

				msgs = append(msgs, msg.Uid)
				r := msg.GetBody(section)
				if err := to.Append(dest, msg.Flags, msg.InternalDate, r); err != nil {
					return err
				}
			}
		}
		item := imap.FormatFlagsOp(imap.AddFlags, true)
		flags := []interface{}{imap.DeletedFlag}
		del := new(imap.SeqSet)
		del.AddNum(msgs...)
		if err := from.UidStore(del, item, flags, nil); err != nil {
			log.Fatal(err)
		}
		if err := from.Expunge(nil); err != nil {
			log.Printf("failed to expunge messages: %s\n", err)
		}

		mbox, err = from.Select(source, false)
		if err != nil {
			return err
		}
	}
	return nil
}

func main() {
	flag.Parse()

	configFilename := os.Getenv("CONFIG_FILE")
	if configFilename == "" {
		wd, _ := os.Getwd()
		configFilename = path.Join(wd, "config.json")
	}
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

	if listOnly {
		mboxSrv := make(chan *imap.MailboxInfo, 10)
		done := make(chan error, 1)
		go func() {
			done <- from.List("", "*", mboxSrv)
		}()
		for m := range mboxSrv {
			fmt.Println(m.Name)
		}
		return
	}

	to, err := createClient(cfg.To)
	if err != nil {
		log.Fatal(err)
	}
	defer to.Close()

	mailboxes := make(map[string]string)

	if len(cfg.Include) != 0 {
		for _, m := range cfg.Include {
			mailboxes[m] = m
		}
	} else {
		mboxSrv := make(chan *imap.MailboxInfo, 10)
		done := make(chan error, 1)
		go func() {
			done <- from.List("", "*", mboxSrv)
		}()
		for m := range mboxSrv {
			mailboxes[m.Name] = m.Name
		}
		if err := <-done; err != nil {
			log.Fatal(err)
		}
	}

	for k, v := range cfg.Mapping {
		_, ok := mailboxes[k]
		if ok {
			mailboxes[k] = v
		}
	}

	if len(cfg.Exclude) != 0 {
		for _, v := range cfg.Exclude {
			delete(mailboxes, v)
		}
	}

	boxesToCopy := make([]string, len(mailboxes))
	idx := 0
	for k := range mailboxes {
		boxesToCopy[idx] = k
		idx++
	}
	sort.Strings(boxesToCopy)

	log.Println("Copying mailboxes:")
	for _, f := range boxesToCopy {
		t := mailboxes[f]
		log.Printf("%s -> %s\n", f, t)
	}

	uiprogress.Start()
	for _, f := range boxesToCopy {
		if err := copyMailbox(from, to, f, mailboxes[f]); err != nil {
			log.Fatal(err)
		}
	}
}

func init() {
	flag.BoolVar(&listOnly, "list", false, "List available mailboxes and then exit")
}
