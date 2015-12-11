package main

import (
	"encoding/json"
	"fmt"
	"github.com/garyburd/go-oauth/oauth"
	"io/ioutil"
	"os"
	"strings"
)

const (
	apiTokenFile  = "apiToken.json"
	userTokenFile = "userToken.json"
)

func init() {
	authInit()
}

func loadToken(filename string) (*oauth.Credentials, error) {
	bytes, err := ioutil.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	var token oauth.Credentials
	if err := json.Unmarshal(bytes, &token); err != nil {
		return nil, err
	}
	return &token, nil
}

func usage() {
	fmt.Println("Usage: ")
	fmt.Println(os.Args[0] + " auth|upload")
}

func main() {
	if len(os.Args) < 2 {
		usage()
		return
	}

	// initialize()

	cmd := strings.ToLower(os.Args[1])
	if cmd == "auth" {
		auth()
	} else if cmd == "upload" {
		upload()
	} else {
		usage()
		return
	}
}
