package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/imroc/req/v3"
	"github.com/thoas/go-funk"
)

type FastAPIResp struct {
	Client struct {
		IP       string `json:"ip"`
		Asn      string `json:"asn"`
		Isp      string `json:"isp"`
		Location struct {
			City    string `json:"city"`
			Country string `json:"country"`
		} `json:"location"`
	} `json:"client"`
	Targets []struct {
		Name     string `json:"name"`
		URL      string `json:"url"`
		Location struct {
			City    string `json:"city"`
			Country string `json:"country"`
		} `json:"location"`
	} `json:"targets"`
}

var urls []*url.URL
var downloader = req.NewClient()
var totalBytes uint64
var bytesPerThread []uint64
var threads int
var respTime []float64
var firstRespTime []float64
var speed []float64
var cliOcaIpVersion string
var cliFastApiIpVersion string

func Request(url *url.URL, path string, callback func(info req.DownloadInfo)) (req.TraceInfo, int, error) {
	req_url := *url
	req_url.Path = req_url.Path + path
	resp, err := downloader.R().SetOutputFile("/dev/null").SetDownloadCallbackWithInterval(callback, 20*time.Millisecond).Get(req_url.String())
	if err != nil {
		fmt.Println(err)
		if err == io.ErrUnexpectedEOF {
			return resp.TraceInfo(), len(resp.Bytes()), nil
		}
		return resp.TraceInfo(), 0, err
	}
	contentLength, err := strconv.Atoi(resp.Header.Values("Content-Length")[0])
	if err != nil {
		return resp.TraceInfo(), 0, err
	}
	return resp.TraceInfo(), contentLength, nil
}

func init() {
	flag.StringVar(&cliOcaIpVersion, "o", "", "Set Connect OCA IP Version")
	flag.StringVar(&cliFastApiIpVersion, "a", "", "Set Connect Fast API IP Version")
	flag.IntVar(&threads, "t", 4, "Set Threads")
	downloader.EnableTraceAll()
	// downloader.EnableDumpAll()
	bytesPerThread = make([]uint64, threads)
	downloader.EnableTraceAll()
}

func main() {
	flag.Parse()
	client := req.C().SetDial(func(ctx context.Context, network, addr string) (net.Conn, error) {
		dialer := &net.Dialer{}
		return dialer.Dial(network+cliFastApiIpVersion, addr)
	})
	downloader.SetDial(func(ctx context.Context, network, addr string) (net.Conn, error) {
		dialer := &net.Dialer{}
		return dialer.Dial(network+cliOcaIpVersion, addr)
	})
	var mutex sync.Mutex
	Fast := FastAPIResp{}
	client.R().SetSuccessResult(&Fast).Get("https://api.fast.com/netflix/speedtest/v2?https=true&token=YXNkZmFzZGxmbnNkYWZoYXNkZmhrYWxm&urlCount=2")
	for _, v := range Fast.Targets {
		parsedURL, _ := url.Parse(v.URL)
		urls = append(urls, parsedURL)
	}
	fmt.Println("ASN:", Fast.Client.Asn, "IP:", Fast.Client.IP, "Location:", Fast.Client.Location.City, Fast.Client.Location.Country)
	fmt.Println("Got URLs:", funk.Map(urls, func(aurl *url.URL) string {
		return aurl.Hostname()
	}))
	for _, v := range urls {
		traceinfo, _, err := Request(v, "/range/0-0", func(info req.DownloadInfo) {})
		if err != nil {
			panic(err)
		}
		fmt.Println("Got Connetion to", v.Hostname(), "in", float64(traceinfo.FirstResponseTime.Nanoseconds())/1e6, "ms")
	}
	var wg sync.WaitGroup
	exitChannel := make(chan int)
	ticker := time.NewTicker(1 * time.Second)
	for i := 0; i < threads; i++ {
		wg.Add(1)
		turl := urls[i%len(urls)]
		go func(url *url.URL, threadID int, exitChannel chan int) {
			defer wg.Done()
			callback := func(info req.DownloadInfo) {
				bytesPerThread[threadID] = uint64(info.DownloadedSize)
			}
			for {
				select {
				case <-exitChannel:
					return
				default:
					traceinfo, detal, _ := Request(url, "/range/0-26214400", callback)
					mutex.Lock()
					totalBytes += uint64(detal)
					respTime = append(respTime, float64(traceinfo.ResponseTime.Nanoseconds())/1e6)
					firstRespTime = append(firstRespTime, float64(traceinfo.FirstResponseTime.Nanoseconds())/1e6)
					mutex.Unlock()
				}
			}
		}(turl, i, exitChannel)
	}
	go func(exitChannel chan int) {
		nowBytes, lastBytes := uint64(0), uint64(0)
		for {
			select {
			case <-exitChannel:
				return
			case <-ticker.C:
				nowBytes = totalBytes + funk.SumUInt64(bytesPerThread)
				detal := nowBytes - lastBytes
				newSpeed := float64(detal) / 1024 / 1024 * 8
				fmt.Printf("\r%.2f Mbps", newSpeed)
				speed = append(speed, newSpeed)
				lastBytes = nowBytes
			}
		}
	}(exitChannel)
	time.Sleep(10 * time.Second)
	ticker.Stop()
	close(exitChannel)
	wg.Wait()
	fmt.Printf("\n-----\nMaxSpeed: %.2f Mbps AvgSpeed: %.2f Mbps MinLatency: %.2f ms\n", funk.MaxFloat64(speed), funk.SumFloat64(speed)/float64(len(speed)), funk.MinFloat64(firstRespTime))
}
