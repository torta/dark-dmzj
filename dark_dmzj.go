package main

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"github.com/labstack/echo"
	"github.com/labstack/echo/middleware"
	"github.com/tidwall/gjson"
	"gopkg.in/cheggaaa/pb.v1"
	"io"
	"io/ioutil"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"time"
)

var client *http.Client
var bar *pb.ProgressBar
var isDownloading bool

type dmzjBook struct {
	ID                    uint64   `json:"id"`
	Title                 string   `json:"title"`
	IsLong                int64    `json:"islong"`
	Authors               []string `json:"authors"`
	Types                 []string `json:"types"`
	Status                []string `json:"status"`
	Cover                 string   `json:"cover"`
	LastUpdateChapterName string   `json:"last_update_chapter_name"`
	LastUpdateChapterID   uint64   `json:"last_update_chapter_id"`
	LastUpdateTime        int64    `json:"last_updatetime"`
}

func init() {
	client = &http.Client{
		Transport: &http.Transport{
			MaxIdleConnsPerHost:   256,
			MaxIdleConns:          256,
			ResponseHeaderTimeout: 10 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			TLSNextProto:          make(map[string]func(authority string, c *tls.Conn) http.RoundTripper),
		},
		Timeout: time.Second * 10,
	}

	ex, err := os.Executable()
	if err != nil {
		panic(err)
	}
	exPath := filepath.Dir(ex)
	if _, err := os.Stat(filepath.Join(exPath, "public", "index.html")); !os.IsNotExist(err) {
		if err := os.Chdir(exPath); err != nil {
			panic(err)
		}
	}
}

func apiWithRetry(id int, try int) string {
	for i := 0; i < try; i++ {
		domains := []string{"v2.api.dmzj.com", "v3api.dmzj.com"}
		res, err := client.Get(fmt.Sprintf("http://%s/comic/%d.json", domains[rand.Intn(2)], id))
		resCk, errCk := client.Get(fmt.Sprintf("https://api.m.dmzj.com/info/%d.html", id))
		if err == nil {
			defer res.Body.Close()
			defer io.Copy(ioutil.Discard, res.Body)
		}
		if errCk == nil {
			defer resCk.Body.Close()
			defer io.Copy(ioutil.Discard, resCk.Body)
		}
		if err == nil && res.StatusCode == 200 {
			body, _ := ioutil.ReadAll(res.Body)
			if errCk == nil && resCk.StatusCode == 200 {
				bodyCk, _ := ioutil.ReadAll(resCk.Body)
				if len(bodyCk) > 1024 {
					return ""
				}
				return string(body)
			}
		}
	}
	return ""
}

func getItem(id int, c chan<- string) {
	c <- apiWithRetry(id, 5)
	bar.Increment()
}

func arrayMap(vs []string, f func(string) []string) [][]string {
	vsm := make([][]string, len(vs))
	for i, v := range vs {
		vsm[i] = f(v)
	}
	return vsm
}

func downloadBooks() {
	if isDownloading {
		return
	}
	isDownloading = true
	defer func() { isDownloading = false }()

	MaxRoutines := 50
	MaxBooks := 60000

	bar = pb.New(MaxBooks - 1).Prefix("Updating ")
	bar.SetWidth(60)
	bar.ShowTimeLeft = true
	bar.ShowCounters = false
	bar.ShowSpeed = true
	bar.Start()
	defer bar.FinishPrint("Finish!")

	c := make(chan string, MaxBooks)
	jobs := make(chan int, MaxBooks)
	for i := 0; i < MaxRoutines; i++ {
		go func() {
			for e := range jobs {
				getItem(e, c)
			}
		}()
	}
	for i := 1; i < MaxBooks; i++ {
		jobs <- i
	}

	items := []dmzjBook{}
	for p := 1; p < MaxBooks; p++ {
		dat := gjson.Parse(<-c)
		if dat.Get("id").Exists() {
			tags := arrayMap([]string{"authors", "types", "status"}, func(v string) []string {
				tag := []string{}
				for _, e := range dat.Get(v).Array() {
					tag = append(tag, e.Get("tag_name").String())
				}
				return tag
			})
			items = append(items, dmzjBook{
				ID:                    dat.Get("id").Uint(),
				Title:                 dat.Get("title").String(),
				IsLong:                dat.Get("islong").Int(),
				Authors:               tags[0],
				Types:                 tags[1],
				Status:                tags[2],
				Cover:                 dat.Get("cover").String(),
				LastUpdateChapterName: dat.Get("chapters.0.data.0.chapter_title").String(),
				LastUpdateChapterID:   dat.Get("chapters.0.data.0.chapter_id").Uint(),
				LastUpdateTime:        dat.Get("last_updatetime").Int(),
			})
		}
	}

	sort.Slice(items, func(a, b int) bool {
		return items[a].LastUpdateTime > items[b].LastUpdateTime
	})

	jsonDat, _ := json.Marshal(items)
	ioutil.WriteFile("public/data.json", jsonDat, 0644)
}

func main() {
	go func() {
		time.Sleep(1 * time.Second)
		downloadBooks()
	}()

	e := echo.New()
	e.HideBanner = true
	e.Static("/", "public")
	e.Use(middleware.GzipWithConfig(middleware.GzipConfig{
		Level: 5,
	}))
	e.GET("/webpic/*", func(c echo.Context) error {
		req, _ := http.NewRequest("GET", fmt.Sprintf("http://images.dmzj.com%s", c.Request().URL.Path), nil)
		req.Header.Set("Referer", "https://m.dmzj.com/")
		res, err := client.Do(req)
		if err != nil {
			return c.NoContent(http.StatusBadGateway)
		}
		defer res.Body.Close()
		data, _ := ioutil.ReadAll(res.Body)
		return c.Blob(res.StatusCode, res.Header.Get("Content-Type"), data)
	})
	e.Logger.Fatal(e.Start(":7777"))
}
