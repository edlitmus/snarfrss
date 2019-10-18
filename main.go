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
	"regexp"
	"strconv"
	"strings"

	yaml "github.com/esilva-everbridge/yaml"
	"github.com/go-redis/redis"
	"github.com/mmcdole/gofeed"
	"github.com/pioz/tvdb"
	"golang.org/x/net/html"
	yamlv3 "gopkg.in/yaml.v3"
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

// Info type
type Info struct {
	Title      string
	Series     string
	Season     int
	Episode    int
	Resolution string
}

var tvdbc tvdb.Client
var db *redis.Client

func main() {
	fp := gofeed.NewParser()
	config := yaml.New()
	configFile := createConfigPath()
	initConfig(configFile, config)

	tvDBConfig := config.Get("the_tvdb_api")
	rpcURL := config.Get("rpc_url").(string)
	username := config.Get("username").(string)
	password := config.Get("password").(string)
	dbConf := config.Get("db")
	feeds := config.Get("feeds")
	shows := config.Get("titles")

	//	fmt.Printf("dbConf: #%v\n", dbConf)

	tvdbc = tvdbClient(tvDBConfig)
	db = newClient(dbConf)

	for _, feed := range feeds.([]interface{}) {
		rss, err := fp.ParseURL(feed.(string))
		if err != nil {
			log.Fatal(err)
		}

		fmt.Println(rss.Title)

	ITEMS:
		for _, item := range rss.Items {
			parts := getKVStringFromTitle(item.Title)
			if parts.Title == "" {
				continue ITEMS
			}
			keyString := fmt.Sprintf("%s-%d-%d-%s", parts.Series, parts.Season, parts.Episode, parts.Resolution)

			for _, t := range shows.([]interface{}) {
				// fmt.Printf("Checking: %s\n", parts.Series)
				r := regexp.MustCompile(fmt.Sprintf("(?mi).*?%s.*?", t.(string)))
				matches := r.MatchString(parts.Series)
				seenIt := exists(keyString, parts)
				if seenIt {
					fmt.Printf("exists: %s\n", keyString)
					continue ITEMS
				}

				if matches && !seenIt {
					r = regexp.MustCompile(`(?mi)(\d+)p`)
					resStrs := r.FindStringSubmatch(parts.Resolution)
					if len(resStrs) > 0 {
						res, _ := strconv.ParseInt(resStrs[1], 10, 0)
						if res > 720 {
							fmt.Printf("Found: %s\n", keyString)
							auth := basicAuth(rpcURL, username, password)
							err := sendMagnet(rpcURL, auth, username, password, item.Link)
							if err != nil {
								log.Println(err)
							}
							parts.Title = getEpisodeTitle(parts.Series, parts.Season, parts.Episode)
							fmt.Printf("PARTS: #%v\n", parts)
							err = seen(keyString, parts)
							if err != nil {
								log.Println(err)
							}
						}
					}
				} else if matches && seenIt {
					fmt.Printf("already processed %s\n", keyString)
					continue ITEMS
				}
			}
		}
	}
}

func tvdbClient(tvDBConfig interface{}) tvdb.Client {
	conf := tvDBConfig.(map[interface{}]interface{})
	c := tvdb.Client{Apikey: conf["api_key"].(string), Userkey: conf["user_key"].(string), Username: conf["username"].(string)}
	err := c.Login()
	if err != nil {
		log.Fatal(err)
	}

	return c
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
	// fmt.Printf("RESP: %#v\n", resp)
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
	err = yamlv3.Unmarshal(buf, &config.Values)
	if err != nil {
		log.Fatal(err)
	}
}

func getEpisodeTitle(series string, season int, episode int) string {
	title := ""
	s, err := tvdbc.BestSearch(series)
	if err != nil {
		log.Printf("Series Search Error: %s\n", err)
	} else {
		err = tvdbc.GetSeriesEpisodes(&s, nil)
		if err != nil {
			log.Printf("Get Episode Error: %s\n", err)
		} else {
			ep := s.GetEpisode(season, episode)
			if ep != nil {
				title = ep.EpisodeName
			}
		}
	}

	return title
}

func getKVStringFromTitle(title string) Info {
	re := regexp.MustCompile(`(?mi)(.*?)\s+S(\d+)E(\d+).*?(\d+p)`)
	parts := re.FindStringSubmatch(title)
	if len(parts) > 0 {
		s, _ := strconv.Atoi(parts[2])
		e, _ := strconv.Atoi(parts[3])
		info := Info{Title: parts[0], Series: parts[1], Season: s, Episode: e, Resolution: parts[4]}
		return info
	}
	return Info{}
}

func seen(key string, show Info) error {
	j, err := show.MarshalBinary()
	if err != nil {
		log.Fatal(err)
	}
	return db.HSet(key, "json", j).Err()
}

func exists(key string, show Info) bool {
	var cached Info
	d, _ := db.HGet(key, "json").Bytes()

	if len(d) == 0 {
		return false
	}

	if err := cached.UnmarshalBinary(d); err != nil {
		log.Fatal(err)
	}

	return true
}

// MarshalBinary turn struct to json
func (e *Info) MarshalBinary() ([]byte, error) {
	return json.Marshal(e)
}

// UnmarshalBinary return struct from json
func (e *Info) UnmarshalBinary(data []byte) error {
	if err := json.Unmarshal(data, &e); err != nil {
		return err
	}

	return nil
}
