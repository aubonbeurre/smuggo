// Copyright 2016 Timothy Gion
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/user"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gomodule/oauth1/oauth"
)

const (
	apiRoot      = "https://api.smugmug.com"
	apiCurUser   = apiRoot + "/api/v2!authuser"
	apiAlbums    = "!albums"
	searchAlbums = apiRoot + "/api/v2/album!search"
	imagesAlbums = apiRoot + "/api/v2/album/%s!images"
)

const albumPageSize = 100

var gAlbums []albumJson
var gImages []imageJson

type uriJson struct {
	Uri string
}

type pagesJson struct {
	Total          int
	Start          int
	Count          int
	RequestedCount int
	NextPage       string
}

type searchAlbumJson struct {
	AlbumKey string
	Name     string
}

type imageJson struct {
	Uri          string
	FileName     string
	Date         string
	ArchivedUri  string
	ArchivedSize int
	ArchivedMD5  string
}

type albumJson struct {
	Uri     string
	UrlName string
}

func (a albumJson) key() string {
	tokens := strings.Split(a.Uri, "/")
	return tokens[len(tokens)-1]
}

// Sort album array by UrlName for printing.
type byUrlName []albumJson

func (b byUrlName) Len() int           { return len(b) }
func (b byUrlName) Swap(i, j int)      { b[i], b[j] = b[j], b[i] }
func (b byUrlName) Less(i, j int) bool { return b[i].UrlName < b[j].UrlName }

type endpointJson struct {
	Album []albumJson
	Pages pagesJson
	User  uriJson
}

type imagesRespJson struct {
	AlbumImage []imageJson
	Pages      pagesJson
}

type albumImages struct {
	AlbumImage []imageJson
	Dir        string
	Album      albumJson
}

// Standard top level response from SmugMug API.
type responseJson struct {
	Response endpointJson
}

type imagesJson struct {
	Response imagesRespJson
}

type searchJson struct {
	Album []searchAlbumJson
	Pages pagesJson
}

// Top level response for search from SmugMug API.
type searchResponseJson struct {
	Response searchJson
}

// getUser retrieves the URI that serves the current user.
func getUser(userToken *oauth.Credentials) (string, error) {
	var queryParams = url.Values{
		"_accept":    {"application/json"},
		"_verbosity": {"1"},
	}
	resp, err := oauthClient.Get(nil, userToken, apiCurUser, queryParams)
	if err != nil {
		log.Println("Error getting user endpoint: " + err.Error())
		return "", err
	}

	defer resp.Body.Close()

	bytes, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Println("Error reading user endpoint: " + err.Error())
		return "", err
	}

	if resp.StatusCode != http.StatusOK {
		fmt.Println("getUser response: " + resp.Status)
	}

	var respJson responseJson
	err = json.Unmarshal(bytes, &respJson)
	if err != nil {
		log.Println("Error decoding user endpoint JSON: " + err.Error())
		return "", err
	}

	if respJson.Response.User.Uri == "" {
		fmt.Println("No Uri object found in getUser response.")
		return "", errors.New("No Uri object found in getUser response.")
	}

	return respJson.Response.User.Uri, nil
}

// printAlbums prints all the albums after sorting alphabetically.
func printAlbums(albums []albumJson) {
	sort.Sort(byUrlName(albums))
	for _, album := range albums {
		fmt.Println(album.UrlName + " :: " + album.key())
	}
	gAlbums = albums
}

// aggregateTerms combines search terms into a single string with each search
// term separated by a plus sign.
func aggregateTerms(terms []string) string {
	var combinedTerms string
	for i, term := range terms {
		combinedTerms += term
		if i < len(terms)-1 {
			combinedTerms += "+"
		}
	}

	return combinedTerms
}

// search is the entry point to album search.
func search(terms []string) {
	userToken, err := loadUserToken()
	if err != nil {
		log.Println("Error reading OAuth token: " + err.Error())
		return
	}

	userUri, err := getUser(userToken)
	if err != nil {
		return
	}

	combinedTerms := aggregateTerms(terms)
	var client = http.Client{}

	searchRequest(&client, userToken, userUri, combinedTerms, 1)
}

// searchRequest sends the search request to SmugMug and asks for the entries beginning at start.
func searchRequest(client *http.Client, userToken *oauth.Credentials, userUri string, query string, start int) {
	var queryParams = url.Values{
		"_accept":       {"application/json"},
		"_verbosity":    {"1"},
		"_filter":       {"Album,Name,AlbumKey"},
		"_filteruri":    {""},
		"Scope":         {userUri},
		"SortDirection": {"Descending"},
		"SortMethod":    {"Rank"},
		"Text":          {query},
		"start":         {fmt.Sprintf("%d", start)},
		"count":         {"15"},
	}

	resp, err := oauthClient.Get(client, userToken, searchAlbums, queryParams)
	if err != nil {
		return
	}

	bytes, err := func() ([]byte, error) {
		defer resp.Body.Close()
		b, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			return nil, err
		}
		return b, nil
	}()

	if err != nil {
		log.Println("Reading search results: " + err.Error())
		return
	}

	var respJson searchResponseJson
	err = json.Unmarshal(bytes, &respJson)
	if err != nil {
		log.Println("Decoding album search endpoint JSON: " + err.Error())
		return
	}

	if len(respJson.Response.Album) < 1 {
		fmt.Println("No search results found.")
		return
	}

	printSearchResults(respJson.Response.Album)

	pages := &respJson.Response.Pages
	if pages.Count+pages.Start < pages.Total {
		fmt.Println("Press Enter for more results or Ctrl-C to quit.")
		var foo string
		fmt.Scanln(&foo)
		searchRequest(client, userToken, userUri, query, pages.Count+pages.Start)
	}
}

// printSearchResults outputs the album names and keys to stdout.
func printSearchResults(results []searchAlbumJson) {
	for _, album := range results {
		fmt.Println(album.Name + " :: " + album.AlbumKey)
	}
}

func getImages(client *http.Client, userToken *oauth.Credentials,
	albumsURI string, start int, count int,
	epChan chan imagesRespJson) {
	var queryParams = url.Values{
		"_accept":    {"application/json"},
		"_verbosity": {"1"},
		"start":      {fmt.Sprintf("%d", start)},
		"count":      {fmt.Sprintf("%d", count)},
	}

	resp, err := oauthClient.Get(client, userToken, albumsURI, queryParams)
	if err != nil {
		return
	}

	bytes, err := func() ([]byte, error) {
		defer resp.Body.Close()
		b, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			return nil, err
		}
		return b, nil
	}()

	if err != nil {
		log.Println("Reading images: " + err.Error())
		return
	}

	//fmt.Println(string(bytes))
	var respJson imagesJson
	err = json.Unmarshal(bytes, &respJson)
	if err != nil {
		log.Println("Decoding images endpoint JSON: " + err.Error())
		return
	}

	if len(respJson.Response.AlbumImage) < 1 {
		//fmt.Println("No images found.")
		//return
	}

	epChan <- respJson.Response
}

func getAllImages() {
	userToken, err := loadUserToken()
	if err != nil {
		log.Println("Error reading OAuth token: " + err.Error())
		return
	}
	var client = &http.Client{}

	allImgs := make([]albumImages, 0)
	usr, _ := user.Current()
	smugmugdir := path.Join(usr.HomeDir, "smugmug")
	for _, album := range gAlbums {
		epChan := make(chan imagesRespJson, 10)
		fmt.Printf("Requesting number of images for %s\n", album.UrlName)
		imagesURI := fmt.Sprintf(imagesAlbums, album.key())
		getImages(client, userToken, imagesURI, 1, 1, epChan)
		ep := <-epChan

		fmt.Printf("%d images in %s\n", ep.Pages.Total, album.UrlName)
		dir := path.Join(smugmugdir, album.UrlName)
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			os.Mkdir(dir, 0666)
		}

		if ep.Pages.Count >= ep.Pages.Total {
			imgs := albumImages{Album: album, AlbumImage: ep.AlbumImage, Dir: dir}
			allImgs = append(allImgs, imgs)
			continue
		}

		waitGrp := sync.WaitGroup{}
		start := ep.Pages.Count + 1

		for start < ep.Pages.Total {
			fmt.Printf("Requesting %d images starting at %d.\n", albumPageSize, start)
			waitGrp.Add(1)
			go func(startInd int) {
				defer waitGrp.Done()
				getImages(client, userToken, imagesURI, startInd, albumPageSize, epChan)
			}(start)
			start += albumPageSize
		}

		albumImgs := make([]imageJson, 0, ep.Pages.Total)
		albumImgs = append(albumImgs, ep.AlbumImage...)

		albumsReqDoneChan := make(chan bool)
		resultsPrintedChan := make(chan bool)
		go collectImageResults(albumImgs, albumsReqDoneChan, epChan,
			resultsPrintedChan)

		waitGrp.Wait()

		// Tell collectImageResults() that all album requests finished.
		albumsReqDoneChan <- true

		// Wait for albums to be displayed.
		<-resultsPrintedChan

		imgs := albumImages{Album: album, AlbumImage: gImages, Dir: dir}
		allImgs = append(allImgs, imgs)
	}
	for _, a := range allImgs {
		fmt.Printf("%d images in %s => %s\n", len(a.AlbumImage), a.Album.UrlName, a.Dir)
	}
}

// collectImageResults receives albums over epChan from getAlbumPage().  It
// continues to listen to epChan until receiving true from albumsReqDoneChan.
// Finally, it outputs the albums to stdout and indicates completion by
// sending true over resultsPrintedChan.
func collectImageResults(
	albumImgs []imageJson,
	albumsReqDoneChan chan bool,
	epChan chan imagesRespJson,
	resultsPrintedChan chan bool) {

	done := false
	for !done || len(epChan) > 0 {
		select {
		case epAlbs := <-epChan:
			albumImgs = append(albumImgs, epAlbs.AlbumImage...)
		case done = <-albumsReqDoneChan:
		}
	}

	//printAlbums(albums)
	gImages = albumImgs
	resultsPrintedChan <- true
}

// albums lists all the albums (and their keys) that belong to the user.
func albums() {
	userToken, err := loadUserToken()
	if err != nil {
		log.Println("Error reading OAuth token: " + err.Error())
		return
	}

	userUri, err := getUser(userToken)
	if err != nil {
		return
	}

	startT := time.Now()
	albumsUri := apiRoot + userUri + apiAlbums
	var client = http.Client{}
	epChan := make(chan endpointJson, 10)
	fmt.Println("Requesting number of albums.")
	getAlbumPage(&client, userToken, albumsUri, 1, 1, epChan)
	ep := <-epChan

	if ep.Pages.Count >= ep.Pages.Total {
		printAlbums(ep.Album)
		return
	}

	waitGrp := sync.WaitGroup{}
	start := ep.Pages.Count + 1

	for start < ep.Pages.Total {
		fmt.Printf("Requesting %d albums starting at %d.\n", albumPageSize, start)
		waitGrp.Add(1)
		go func(startInd int) {
			defer waitGrp.Done()
			getAlbumPage(&client, userToken, albumsUri, startInd, albumPageSize, epChan)
		}(start)
		start += albumPageSize
	}

	albums := make([]albumJson, 0, ep.Pages.Total)
	copy(albums, ep.Album)

	albumsReqDoneChan := make(chan bool)
	resultsPrintedChan := make(chan bool)
	go collectAlbumResults(albums, albumsReqDoneChan, epChan,
		resultsPrintedChan)

	waitGrp.Wait()

	// Tell collectAlbumResults() that all album requests finished.
	albumsReqDoneChan <- true

	// Wait for albums to be displayed.
	<-resultsPrintedChan
	totalT := time.Since(startT)
	fmt.Println("\nElapsed time: " + totalT.String())
}

// collectAlbumResults receives albums over epChan from getAlbumPage().  It
// continues to listen to epChan until receiving true from albumsReqDoneChan.
// Finally, it outputs the albums to stdout and indicates completion by
// sending true over resultsPrintedChan.
func collectAlbumResults(
	albums []albumJson,
	albumsReqDoneChan chan bool,
	epChan chan endpointJson,
	resultsPrintedChan chan bool) {

	done := false
	for !done || len(epChan) > 0 {
		select {
		case epAlbs := <-epChan:
			albums = append(albums, epAlbs.Album...)
		case done = <-albumsReqDoneChan:
		}
	}

	printAlbums(albums)
	resultsPrintedChan <- true
}

// getAlbumPage gets up to count albums starting at index start.  It returns
// the album and page data over epChan, so it may be invoked as a goroutine.
func getAlbumPage(
	client *http.Client, userToken *oauth.Credentials,
	albumsUri string, start int, count int,
	epChan chan endpointJson) {

	var queryParams = url.Values{
		"_accept":    {"application/json"},
		"_verbosity": {"1"},
		"start":      {fmt.Sprintf("%d", start)},
		"count":      {fmt.Sprintf("%d", count)},
	}

	resp, err := oauthClient.Get(client, userToken, albumsUri, queryParams)
	if err != nil {
		return
	}

	bytes, err := func() ([]byte, error) {
		defer resp.Body.Close()
		b, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			return nil, err
		}
		return b, nil
	}()

	if err != nil {
		log.Println("Reading albums: " + err.Error())
		return
	}

	var respJson responseJson
	err = json.Unmarshal(bytes, &respJson)
	if err != nil {
		log.Println("Decoding album endpoint JSON: " + err.Error())
		return
	}

	if len(respJson.Response.Album) < 1 {
		fmt.Println("No albums found.")
		return
	}

	epChan <- respJson.Response
}

// createAlbum was test code for exercising the SmugMug API.  It works, but is
// hard coded for a particular album in a particular location.
func createAlbum(client *http.Client, credentials *oauth.Credentials) {
	createUri := apiRoot + "/api/v2/node/R3gfM!children"

	var body = map[string]string{
		"Type":    "Album",
		"Name":    "Test Post Create",
		"UrlName": "Test-Post-Create",
		"Privacy": "Public",
	}

	rawJson, err := json.Marshal(body)
	if err != nil {
		return
	}
	fmt.Println(string(rawJson))

	req, err := http.NewRequest("POST", createUri, bytes.NewReader(rawJson))
	if err != nil {
		return
	}

	req.Header["Content-Type"] = []string{"application/json"}
	req.Header["Content-Length"] = []string{fmt.Sprintf("%d", len(rawJson))}
	req.Header["Accept"] = []string{"application/json"}

	if err := oauthClient.SetAuthorizationHeader(
		req.Header, credentials, "POST", req.URL, url.Values{}); err != nil {
		// req.Header, credentials, "POST", req.URL, headers); err != nil {
		return
	}

	fmt.Println(req)

	var resp *http.Response
	resp, err = client.Do(req)
	if err != nil {
		return
	}

	defer resp.Body.Close()

	bytes, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return
	}

	fmt.Println(resp.Status)
	fmt.Println(string(bytes))
}
