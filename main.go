package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"regexp"
	"strings"

	yaml "github.com/esilva-everbridge/yaml"
	"github.com/mmcdole/gofeed"
	"golang.org/x/net/html"
	yamlv2 "gopkg.in/yaml.v2"
)

// {\"method\":\"torrent-add\",\"arguments\":{\"paused\":false,\"filename\":\"%s\"}}

// Args type
type Args struct {
	Paused   bool   `json:"paused"`
	Filename string `json:"filename"`
}

// Request type
type Request struct {
	Method    string `json:"method"`
	Arguments Args   `json:"arguments"`
}

func main() {
	config := yaml.New()

	buf, err := ioutil.ReadFile("snarf.yml")
	if err != nil {
		log.Fatal(err)
	}
	err = yamlv2.Unmarshal(buf, &config.Values)
	if err != nil {
		log.Fatal(err)
	}

	fp := gofeed.NewParser()

	rpcURL := config.Get("rpc_url").(string)
	username := config.Get("username").(string)
	password := config.Get("password").(string)
	feeds := config.Get("feeds")
	shows := config.Get("shows")

	for _, feed := range feeds.([]interface{}) {
		rss, err := fp.ParseURL(feed.(string))
		if err != nil {
			log.Fatal(err)
		}

		fmt.Println(rss.Title)
		for _, item := range rss.Items {
			for _, t := range shows.([]interface{}) {
				r := regexp.MustCompile(fmt.Sprintf("(?i).*?%s.*?", t.(string)))

				if r.MatchString(item.Title) {
					fmt.Printf("Found: %s\n", item.Title)
					auth := basicAuth(rpcURL, username, password)
					err := sendMagnet(rpcURL, auth, username, password, item.Link)
					if err != nil {
						log.Println(err)
					}
				}
			}
		}
	}
}

func sendMagnet(url string, auth string, user string, pass string, link string) error {
	jsonArgs := Args{false, link}
	jsonReq := Request{"torrent-add", jsonArgs}
	postJSON, err := json.Marshal(jsonReq)
	if err != nil {
		fmt.Println("error:", err)
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(postJSON))
	if err != nil {
		log.Fatal(err)
	}
	authParts := strings.Split(auth, ":")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Add(strings.Trim(authParts[0], " "), strings.Trim(authParts[1], " "))
	req.SetBasicAuth(user, pass)
	client := &http.Client{}
	fmt.Printf("%#v\n", req)
	resp, err := client.Do(req)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%#v\n", resp)
	err = resp.Body.Close()
	if err != nil {
		log.Fatal(err)
	}

	return err
}

func basicAuth(url string, username string, password string) string {
	client := &http.Client{}
	req, err := http.NewRequest("POST", url, nil)
	if err != nil {
		log.Fatal(err)
	}

	req.SetBasicAuth(username, password)
	resp, err := client.Do(req)
	if err != nil {
		log.Fatal(err)
	}

	bodyText, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Fatal(err)
	}

	reader := strings.NewReader(string(bodyText))
	z := html.NewTokenizer(reader)

	depth := 0
	for {
		tt := z.Next()
		switch tt {
		case html.ErrorToken:
			return fmt.Sprint(z.Err())
		case html.TextToken:
			if depth > 0 {
				return fmt.Sprint(string(z.Text()))
			}
		case html.StartTagToken, html.EndTagToken:
			tn, _ := z.TagName()
			buffer := bytes.NewBuffer(tn)
			if len(tn) == 4 && buffer.String() == "code" {
				if tt == html.StartTagToken {
					depth++
				} else {
					depth--
				}
			}
		}
	}
}
