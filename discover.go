package main

import (
	"fmt"
	"net"
	"sort"
	"sync"
	"time"
)

// localHosts devuelve las direcciones de las /24 de todas las IPv4 activas que
// no son loopback, sin incluir las de la propia maquina.
func localHosts(port int) []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var self = map[string]bool{}
	var bases [][4]byte
	seenNet := map[string]bool{}
	for _, ifi := range ifaces {
		if ifi.Flags&net.FlagUp == 0 || ifi.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifi.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip4 := ipnet.IP.To4()
			if ip4 == nil {
				continue
			}
			self[ip4.String()] = true
			var base [4]byte
			copy(base[:], ip4.Mask(net.CIDRMask(24, 32)))
			if !seenNet[string(base[:])] {
				seenNet[string(base[:])] = true
				bases = append(bases, base)
			}
		}
	}
	var out []string
	for _, b := range bases {
		for i := 1; i < 255; i++ {
			ip := fmt.Sprintf("%d.%d.%d.%d", b[0], b[1], b[2], i)
			if self[ip] {
				continue
			}
			out = append(out, fmt.Sprintf("%s:%d", ip, port))
		}
	}
	return out
}

// probeHosts prueba el setup QUII en paralelo y devuelve las direcciones que
// responden como un timbre.
func probeHosts(hosts []string, timeout time.Duration, workers int) []string {
	if workers <= 0 {
		workers = 32
	}
	jobs := make(chan string)
	var mu sync.Mutex
	var found []string
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for h := range jobs {
				if err := ProbeQuii(h, timeout); err == nil {
					mu.Lock()
					found = append(found, h)
					mu.Unlock()
				}
			}
		}()
	}
	for _, h := range hosts {
		jobs <- h
	}
	close(jobs)
	wg.Wait()
	sort.Strings(found)
	return found
}

// discoverDoorbell escanea la LAN local en busca del timbre.
func discoverDoorbell(port int, timeout time.Duration) []string {
	return probeHosts(localHosts(port), timeout, 32)
}
