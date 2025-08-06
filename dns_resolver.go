// Copyright 2018 Ben Kochie <superq@gmail.com>
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/miekg/dns"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	dnsResolveTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "smokeping_prober",
			Name:      "dns_resolve_total",
			Help:      "Total number of DNS resolutions attempted.",
		},
		[]string{"host", "status"},
	)

	dnsResolveDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "smokeping_prober",
			Name:      "dns_resolve_duration_seconds",
			Help:      "Time spent on DNS resolution.",
			Buckets:   prometheus.DefBuckets,
		},
		[]string{"host"},
	)

	dnsCacheEntries = promauto.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "smokeping_prober",
			Name:      "dns_cache_entries",
			Help:      "Number of entries in the DNS cache.",
		},
	)
)

// DNSEntry represents a cached DNS resolution result with TTL information
type DNSEntry struct {
	IPs        []net.IP
	ResolvedAt time.Time
	TTL        time.Duration
	mu         sync.RWMutex
}

// IsExpired checks if the DNS entry has expired based on TTL
func (e *DNSEntry) IsExpired() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return time.Since(e.ResolvedAt) > e.TTL
}

// GetIPs returns the cached IP addresses if not expired
func (e *DNSEntry) GetIPs() ([]net.IP, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if time.Since(e.ResolvedAt) > e.TTL {
		return nil, false
	}
	// Return a copy to avoid race conditions
	ips := make([]net.IP, len(e.IPs))
	copy(ips, e.IPs)
	return ips, true
}

// Update updates the DNS entry with new IPs and TTL
func (e *DNSEntry) Update(ips []net.IP, ttl time.Duration) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.IPs = make([]net.IP, len(ips))
	copy(e.IPs, ips)
	e.ResolvedAt = time.Now()
	e.TTL = ttl
}

// TTLAwareDNSResolver provides DNS resolution with TTL tracking and automatic re-resolution
type TTLAwareDNSResolver struct {
	cache         map[string]*DNSEntry
	mu            sync.RWMutex
	client        *dns.Client
	resolvers     []string
	minTTL        time.Duration
	maxTTL        time.Duration
	forceInterval time.Duration // Override TTL with fixed interval
	stopCh        chan struct{}
	wg            sync.WaitGroup
}

// NewTTLAwareDNSResolver creates a new TTL-aware DNS resolver
func NewTTLAwareDNSResolver(forceInterval time.Duration) *TTLAwareDNSResolver {
	resolver := &TTLAwareDNSResolver{
		cache:         make(map[string]*DNSEntry),
		client:        &dns.Client{Timeout: 5 * time.Second},
		resolvers:     []string{"8.8.8.8:53", "1.1.1.1:53"}, // Default to public DNS
		minTTL:        30 * time.Second,                     // Minimum TTL to respect
		maxTTL:        24 * time.Hour,                       // Maximum TTL to respect
		forceInterval: forceInterval,
		stopCh:        make(chan struct{}),
	}

	// Try to get system DNS resolvers
	if config, err := dns.ClientConfigFromFile("/etc/resolv.conf"); err == nil && len(config.Servers) > 0 {
		var systemResolvers []string
		for _, server := range config.Servers {
			systemResolvers = append(systemResolvers, net.JoinHostPort(server, config.Port))
		}
		resolver.resolvers = systemResolvers
	}

	// Start background cleanup goroutine
	resolver.wg.Add(1)
	go resolver.cleanupExpiredEntries()

	return resolver
}

// Resolve performs DNS resolution with TTL tracking
func (r *TTLAwareDNSResolver) Resolve(hostname string) (net.IP, error) {
	// Check cache first
	r.mu.RLock()
	entry, exists := r.cache[hostname]
	r.mu.RUnlock()

	if exists {
		if ips, valid := entry.GetIPs(); valid && len(ips) > 0 {
			// Return the first IP (similar to net.ResolveIPAddr behavior)
			return ips[0], nil
		}
	}

	// Perform DNS lookup
	ips, ttl, err := r.lookupWithTTL(hostname)
	if err != nil {
		dnsResolveTotal.WithLabelValues(hostname, "error").Inc()
		return nil, fmt.Errorf("DNS resolution failed for %s: %w", hostname, err)
	}

	if len(ips) == 0 {
		dnsResolveTotal.WithLabelValues(hostname, "no_records").Inc()
		return nil, fmt.Errorf("no IP addresses found for %s", hostname)
	}

	// Use forced interval if configured, otherwise use TTL with bounds
	effectiveTTL := ttl
	if r.forceInterval > 0 {
		effectiveTTL = r.forceInterval
	} else {
		if effectiveTTL < r.minTTL {
			effectiveTTL = r.minTTL
		}
		if effectiveTTL > r.maxTTL {
			effectiveTTL = r.maxTTL
		}
	}

	// Update cache
	r.mu.Lock()
	if entry == nil {
		entry = &DNSEntry{}
		r.cache[hostname] = entry
	}
	r.mu.Unlock()

	entry.Update(ips, effectiveTTL)
	dnsCacheEntries.Set(float64(len(r.cache)))
	dnsResolveTotal.WithLabelValues(hostname, "success").Inc()

	logger.Debug("DNS resolved", "hostname", hostname, "ip", ips[0], "ttl", effectiveTTL, "original_ttl", ttl)

	return ips[0], nil
}

// lookupWithTTL performs the actual DNS lookup and extracts TTL information
func (r *TTLAwareDNSResolver) lookupWithTTL(hostname string) ([]net.IP, time.Duration, error) {
	start := time.Now()
	defer func() {
		dnsResolveDuration.WithLabelValues(hostname).Observe(time.Since(start).Seconds())
	}()

	// Try to parse as IP first
	if ip := net.ParseIP(hostname); ip != nil {
		return []net.IP{ip}, 24 * time.Hour, nil // Static IP, use max TTL
	}

	// Try each resolver
	for _, resolver := range r.resolvers {
		ips, ttl, err := r.queryResolver(hostname, resolver)
		if err == nil && len(ips) > 0 {
			return ips, ttl, nil
		}
		logger.Debug("DNS resolver failed", "hostname", hostname, "resolver", resolver, "error", err)
	}

	return nil, 0, fmt.Errorf("all DNS resolvers failed for %s", hostname)
}

// queryResolver queries a specific DNS resolver
func (r *TTLAwareDNSResolver) queryResolver(hostname, resolver string) ([]net.IP, time.Duration, error) {
	// Create DNS message for A record
	msg := new(dns.Msg)
	msg.SetQuestion(dns.Fqdn(hostname), dns.TypeA)
	msg.RecursionDesired = true

	resp, _, err := r.client.Exchange(msg, resolver)
	if err != nil {
		return nil, 0, err
	}

	if resp.Rcode != dns.RcodeSuccess {
		return nil, 0, fmt.Errorf("DNS query failed with rcode: %s", dns.RcodeToString[resp.Rcode])
	}

	var ips []net.IP
	var minTTL uint32 = ^uint32(0) // Max uint32 value

	// Extract A records
	for _, answer := range resp.Answer {
		if a, ok := answer.(*dns.A); ok {
			ips = append(ips, a.A)
			if a.Header().Ttl < minTTL {
				minTTL = a.Header().Ttl
			}
		}
	}

	// Also try AAAA records if no A records found
	if len(ips) == 0 {
		msg.Question[0].Qtype = dns.TypeAAAA
		resp, _, err = r.client.Exchange(msg, resolver)
		if err == nil && resp.Rcode == dns.RcodeSuccess {
			for _, answer := range resp.Answer {
				if aaaa, ok := answer.(*dns.AAAA); ok {
					ips = append(ips, aaaa.AAAA)
					if aaaa.Header().Ttl < minTTL {
						minTTL = aaaa.Header().Ttl
					}
				}
			}
		}
	}

	if len(ips) == 0 {
		return nil, 0, fmt.Errorf("no IP addresses found")
	}

	ttl := time.Duration(minTTL) * time.Second
	return ips, ttl, nil
}

// cleanupExpiredEntries removes expired entries from the cache
func (r *TTLAwareDNSResolver) cleanupExpiredEntries() {
	defer r.wg.Done()
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			r.mu.Lock()
			for hostname, entry := range r.cache {
				if entry.IsExpired() {
					delete(r.cache, hostname)
				}
			}
			dnsCacheEntries.Set(float64(len(r.cache)))
			r.mu.Unlock()
		case <-r.stopCh:
			return
		}
	}
}

// GetCachedIP returns a cached IP for the hostname if available and not expired
func (r *TTLAwareDNSResolver) GetCachedIP(hostname string) (net.IP, bool) {
	r.mu.RLock()
	entry, exists := r.cache[hostname]
	r.mu.RUnlock()

	if !exists {
		return nil, false
	}

	if ips, valid := entry.GetIPs(); valid && len(ips) > 0 {
		return ips[0], true
	}

	return nil, false
}

// Stop stops the DNS resolver and cleanup goroutines
func (r *TTLAwareDNSResolver) Stop() {
	close(r.stopCh)
	r.wg.Wait()
}

// GetCacheStats returns statistics about the DNS cache
func (r *TTLAwareDNSResolver) GetCacheStats() map[string]interface{} {
	r.mu.RLock()
	defer r.mu.RUnlock()

	stats := map[string]interface{}{
		"total_entries": len(r.cache),
		"entries":       make(map[string]interface{}),
	}

	entries := stats["entries"].(map[string]interface{})
	for hostname, entry := range r.cache {
		entry.mu.RLock()
		entries[hostname] = map[string]interface{}{
			"ips":         entry.IPs,
			"resolved_at": entry.ResolvedAt,
			"ttl":         entry.TTL,
			"expired":     entry.IsExpired(),
		}
		entry.mu.RUnlock()
	}

	return stats
}
