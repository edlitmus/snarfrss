package main

import (
	"fmt"

	"github.com/mmcdole/gofeed"
)

func main() {
	fp := gofeed.NewParser()
	// feed, _ := fp.ParseURL("https://iptorrents.com/torrents/rss?u=1695190;tp=ba24b2d29ce9dc265dbf805e9e7a0fb1;101;89;90;48;62;100;7;23;5;99;4;download")
	feed, _ := fp.ParseURL("http://feeds.feedburner.com/eztv-rss-atom-feeds?format=xml")
	fmt.Println(feed.Title)

	for _, item := range feed.Items {
		fmt.Println(item.Title)
	}
}
