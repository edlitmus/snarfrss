package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"


	"github.com/go-redis/redis"
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

var db *redis.Client

func main() {
	fp := gofeed.NewParser()
	config := yaml.New()
	configFile := createConfigPath()
	initConfig(configFile, config)

	rpcURL := config.Get("rpc_url").(string)
	username := config.Get("username").(string)
	password := config.Get("password").(string)
	dbConf := config.Get("db")
	feeds := config.Get("feeds")
	shows := config.Get("titles")

//	fmt.Printf("dbConf: #%v\n", dbConf)

	db = newClient(dbConf)

	for _, feed := range feeds.([]interface{}) {
		rss, err := fp.ParseURL(feed.(string))
		if err != nil {
			log.Fatal(err)
		}

		fmt.Println(rss.Title)
		for _, item := range rss.Items {
			for _, t := range shows.([]interface{}) {
				r := regexp.MustCompile(fmt.Sprintf("(?mi).*?%s.*?", t.(string)))
				parts := getKVStringFromTitle(item.Title)
				if len(parts) == 0 {
					continue
				}
				keyString := parts["title"].(string)
//				fmt.Printf("key: %s\n", keyString)
				seenIt := exists(keyString, parts)
				matches := r.MatchString(keyString)

				if matches && !seenIt {
					r = regexp.MustCompile(`(?mi)(\d+)p`)
					resStrs := r.FindStringSubmatch(parts["resolution"].(string))
					if len(resStrs) > 0 {
						res, _ := strconv.ParseInt(resStrs[1], 10, 0)
						if res > 720 {
							fmt.Printf("Found: %s\n", keyString)
							auth := basicAuth(rpcURL, username, password)
							err := sendMagnet(rpcURL, auth, username, password, item.Link)
							if err != nil {
								log.Println(err)
							}
							err = seen(keyString, parts)
							if err != nil {
								log.Println(err)
							}
						}
					}
				} else if matches && seenIt {
					fmt.Printf("already processed %s\n", keyString)
					break
				}
			}
		}
	}
}

func newClient(dbConfig interface{}) *redis.Client {
	conf := dbConfig.(map[interface{}]interface{})
	client := redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%d", conf["host"].(string), conf["port"].(int)),
		Password: fmt.Sprintf("%s", conf["pass"].(string)),
		DB:       conf["id"].(int),
	})

	_, err := client.Ping().Result()
	if err != nil {
		log.Fatal(err)
	}

	return client
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
	// fmt.Printf("%#v\n", req)
	resp, err := client.Do(req)
	if err != nil {
		log.Fatal(err)
	}
	// fmt.Printf("%#v\n", resp)
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

	return parseCode(z)
}

func parseCode(z *html.Tokenizer) string {
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

func createConfigPath() string {
	var usr, _ = user.Current()
	configFile := filepath.Join(usr.HomeDir, ".config/snarfrss/config.yaml")
	dir := filepath.Dir(configFile)
	err := os.MkdirAll(dir, 0700)
	if err != nil {
		log.Printf("error creating config file path: %s", err)
	}

	return configFile
}

func initConfig(configFile string, config *yaml.Yaml) {
	buf, err := ioutil.ReadFile(configFile)
	if err != nil {
		log.Fatal(err)
	}
	err = yamlv2.Unmarshal(buf, &config.Values)
	if err != nil {
		log.Fatal(err)
	}
}

func getKVStringFromTitle(title string) map[string]interface{} {
	re  := regexp.MustCompile(`(?mi)(.*?)\s+S(\d+)E(\d+).*?(\d+p)`)
	parts := re.FindStringSubmatch(title)
	fmt.Printf("#%v\n", parts)
	if len(parts) > 0 {
		info := make(map[string]interface{})
		info["title"] 		= parts[0]
		info["series"] 		= parts[1]
		info["season"] 		= parts[2]
		info["episode"] 	= parts[3]
		info["resolution"] 	= parts[4]
		return info
	}
	return nil
}

func seen(k string, v map[string]interface{}) error {
	return db.HMSet(k, v).Err()
}

func exists(k string, v map[string]interface{}) bool {
	keys := reflect.ValueOf(v).MapKeys()
	var args []string
	for _, key := range keys {
		args = append(args, key.String())
	}
	d, err := db.HMGet(k, args...).Result()
//	fmt.Printf("DATA: #%v\n", d)
	if d[0] == nil {
		return false
	} else if err == redis.Nil {
		return true
	} else if err != nil {
		log.Fatal(err)
	}

	return err == nil
}
