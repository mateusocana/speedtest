package sthttp

import (
	"net/http"
	"log"
	"io/ioutil"
	"encoding/xml"
	"time"
	"sort"
	"strings"
	"bytes"
	"fmt"
)

import (
	"speedtest/debug"
	"speedtest/misc"
	"speedtest/stxml"
	"speedtest/coords"
)

var SpeedtestConfigUrl = "http://www.speedtest.net/speedtest-config.php"
var SpeedtestServersUrl = "http://www.speedtest.net/speedtest-servers.php"
var CONFIG Config

type Config struct {
	Ip string
	Lat float64
	Lon float64
	Isp string
}

type Server struct {
	Url        string
	Lat        float64
	Lon        float64
	Name       string
	Country    string
	CC         string
	Sponsor    string
	Id         string
	Distance   float64
	//AvgLatency time.Duration
	AvgLatency float64
}

// Sort by Distance
type ByDistance []Server

func (this ByDistance) Len() int {
	return len(this)
}

func (this ByDistance) Less(i, j int) bool {
	return this[i].Distance < this[j].Distance
}

func (this ByDistance) Swap(i, j int) {
	this[i], this[j] = this[j], this[i]
}

// Sort by latency
type ByLatency []Server

func (this ByLatency) Len() int {
	return len(this)
}

func (this ByLatency) Less(i, j int) bool {
	return this[i].AvgLatency < this[j].AvgLatency
}

func (this ByLatency) Swap(i, j int) {
	this[i], this[j] = this[j], this[i]
}

// Check http response
func checkHttp(resp *http.Response) bool {
	var ok bool
	if resp.StatusCode != 200 {
		ok = false
	} else {
		ok = true
	}
	return ok
}

// Download config from speedtest.net
func GetConfig() Config {
	resp, err := http.Get(SpeedtestConfigUrl)
	if err != nil {
		log.Fatalf("Couldn't retrieve our config from speedtest.net: 'Could not create connection'\n")
	}
	defer resp.Body.Close()
	if checkHttp(resp) != true {
		log.Fatalf("Couldn't retrieve our config from speedtest.net: '%s'\n", resp.Status)
	}
	
	body, err2 := ioutil.ReadAll(resp.Body)
	if err2 != nil {
		log.Fatalf("Couldn't retrieve our config from speedtest.net: 'Cannot read body'\n")
	}

	cx := new(stxml.XMLConfigSettings)
	
	err3 := xml.Unmarshal(body, &cx)
	if err3 != nil {
		log.Fatalf("Couldn't retrieve our config from speedtest.net: 'Cannot unmarshal XML'\n")
	}

	c := new(Config)
	c.Ip = cx.Client.Ip
	c.Lat = misc.ToFloat(cx.Client.Lat)
	c.Lon = misc.ToFloat(cx.Client.Lon)
	c.Isp = cx.Client.Isp
	
	if debug.DEBUG { fmt.Printf("Config: %v\n", c) }

	return *c
}

// Download server list from speedtest.net
func GetServers() []Server {
	var servers []Server

	resp, err := http.Get(SpeedtestServersUrl)
	if err != nil {
		log.Fatalf("Cannot get servers list from speedtest.net: 'Cannot contact server'\n")
	}
	defer resp.Body.Close()

	body, err2 := ioutil.ReadAll(resp.Body)
	if err2 != nil {
		log.Fatalf("Cannot get servers list from speedtest.net: 'Cannot read body'\n")
	}

	s := new(stxml.ServerSettings)
	
	err3 := xml.Unmarshal(body, &s)
	if err3 != nil {
		log.Fatalf("Cannot get servers list from speedtest.net: 'Cannot unmarshal XML'\n")
	}

	
	for xmlServer := range s.ServersContainer.XMLServers {
		server := new(Server)
		server.Url = s.ServersContainer.XMLServers[xmlServer].Url
		server.Lat = misc.ToFloat(s.ServersContainer.XMLServers[xmlServer].Lat)
		server.Lon = misc.ToFloat(s.ServersContainer.XMLServers[xmlServer].Lon)
		server.Name = s.ServersContainer.XMLServers[xmlServer].Name
		server.Country = s.ServersContainer.XMLServers[xmlServer].Country
		server.CC = s.ServersContainer.XMLServers[xmlServer].CC
		server.Sponsor = s.ServersContainer.XMLServers[xmlServer].Sponsor
		server.Id = s.ServersContainer.XMLServers[xmlServer].Id
		servers = append(servers, *server)
	}
	return servers
}


func GetClosestServers(numServers int, servers []Server) []Server {
	if debug.DEBUG{ log.Printf("Finding %d closest servers...\n", numServers) }
	// calculate all servers distance from us and save them
	mylat    := CONFIG.Lat
	mylon    := CONFIG.Lon
	myCoords := coords.Coordinate{Lat:mylat, Lon:mylon}
	for server := range servers {
		theirlat := servers[server].Lat
		theirlon := servers[server].Lon
		theirCoords := coords.Coordinate{Lat:theirlat, Lon:theirlon}

		servers[server].Distance = coords.HsDist(coords.DegPos(myCoords.Lat, myCoords.Lon), coords.DegPos(theirCoords.Lat, theirCoords.Lon))
	}
	
	// sort by distance
	sort.Sort(ByDistance(servers))
	
	// return the top X
	return servers[:numServers]
}

func getLatencyUrl(server Server) string {
	u := server.Url
	splits := strings.Split(u, "/")
	baseUrl := strings.Join(splits[1:len(splits) -1], "/")
	latencyUrl := "http:/" + baseUrl + "/latency.txt"
	return latencyUrl
}

// FIXME: this is ugly but I don't have a good alternative right now
// if we were not able truly measure the latency don't bail out
// just set the latency ridiculously high so it isn't choosen
// https://github.com/zpeters/speedtest/issues/5
//func GetLatency(server Server, numRuns int) time.Duration {
func GetLatency(server Server, numRuns int) float64 {
	var latency time.Duration
	var failed bool = false
	var latencyAcc time.Duration
	
	for i := 0; i < numRuns; i++ {
		latencyUrl := getLatencyUrl(server)
		if debug.DEBUG { log.Printf("Testing latency: %s (%s)\n", server.Name, server.Sponsor) }
		
		start := time.Now()
		resp, err := http.Get(latencyUrl)
		if err != nil {
			log.Printf("Cannot test latency of '%s' - 'Cannot contact server'\n", latencyUrl) 
			failed = true
		}
		defer resp.Body.Close()
		
		content, err2 := ioutil.ReadAll(resp.Body)
		if err2 != nil {
			log.Printf("Cannot test latency of '%s' - 'Cannot read body'\n", latencyUrl) 
			failed = true
		}
		
		finish := time.Now()

		if strings.TrimSpace(string(content)) == "test=test" {
			latency = finish.Sub(start)
		} else {
			log.Printf("Server didn't return 'test=test', possibly invalid")
			failed = true
		}

		if failed == true {
			latency = 1 * time.Minute
		}
		
		if debug.DEBUG { log.Printf("\tRun took: %v\n", latency) }
		
		latencyAcc = latencyAcc + latency
	}
	// We want ms not nsP
	return float64(time.Duration(latencyAcc.Nanoseconds() / int64(numRuns)) * time.Nanosecond)/1000000
}

func GetFastestServer(numRuns int, servers []Server) Server {
	for server := range servers {
		avgLatency := GetLatency(servers[server], numRuns)
		
		if debug.DEBUG { log.Printf("Total runs took: %v\n", avgLatency) }
		servers[server].AvgLatency = avgLatency
	}

	sort.Sort(ByLatency(servers))
		
	return servers[0]
}


func DownloadSpeed(url string) float64 {
	start := time.Now()
	resp, err := http.Get(url)
	if err != nil {
		log.Fatalf("Cannot test download speed of '%s' - 'Cannot contact server'\n", url)
	}
	defer resp.Body.Close()
	data, err2 := ioutil.ReadAll(resp.Body)
	if err2 != nil {
		log.Fatalf("Cannot test download speed of '%s' - 'Cannot read body'\n", url)
	}
	finish := time.Now()
 	megabytes := float64(len(data)) / float64(1024) / float64(1024)
	seconds := finish.Sub(start).Seconds()
	if debug.DEBUG { log.Printf("Downloaded %f megabytes\n", megabytes) }
	if debug.DEBUG { log.Printf("Downloaded in %f seconds\n", float64(seconds)) }
	mbps := (megabytes * 8) / float64(seconds)

	return mbps
}

func UploadSpeed(url string, mimetype string, data []byte) float64 {
	start := time.Now()
	buf := bytes.NewBuffer(data)
	resp, err := http.Post(url, mimetype, buf)
	if err != nil {
		log.Fatalf("Cannot test upload speed of '%s' - 'Cannot contact server'\n", url)
	}
	defer resp.Body.Close()
	_, err2 := ioutil.ReadAll(resp.Body)
	if err2 != nil {
		log.Fatalf("Cannot test upload speed of '%s' - 'Cannot read body'\n", url)
	}
	finish := time.Now()
	megabytes := float64(len(data)) / float64(1024) / float64(1024)
	seconds := finish.Sub(start).Seconds()
	if debug.DEBUG { log.Printf("Uploaded %f megabytes\n", megabytes) }
	if debug.DEBUG { log.Printf("Uploaded in %f seconds\n", float64(seconds)) }
	mbps := (megabytes * 8) / float64(seconds)

	return mbps
}
