package kademlia

import (
	"sync"
	"time"
)

// RecentBatchStoreEntry captures a handled BatchStoreData request outcome
type RecentBatchStoreEntry struct {
	TimeUnix   int64  `json:"time_unix"`
	SenderID   string `json:"sender_id"`
	SenderIP   string `json:"sender_ip"`
	Keys       int    `json:"keys"`
	DurationMS int64  `json:"duration_ms"`
	OK         bool   `json:"ok"`
	Error      string `json:"error,omitempty"`
}

// RecentBatchRetrieveEntry captures a handled BatchGetValues request outcome
type RecentBatchRetrieveEntry struct {
	TimeUnix   int64  `json:"time_unix"`
	SenderID   string `json:"sender_id"`
	SenderIP   string `json:"sender_ip"`
	Requested  int    `json:"requested"`
	Found      int    `json:"found"`
	DurationMS int64  `json:"duration_ms"`
	Error      string `json:"error,omitempty"`
}

func (s *Network) appendStoreEntry(ip string, e RecentBatchStoreEntry) {
	s.recentMu.Lock()
	defer s.recentMu.Unlock()
	if s.recentStoreByIP == nil {
		s.recentStoreByIP = make(map[string][]RecentBatchStoreEntry)
	}
	s.recentStoreOverall = append([]RecentBatchStoreEntry{e}, s.recentStoreOverall...)
	if len(s.recentStoreOverall) > 10 {
		s.recentStoreOverall = s.recentStoreOverall[:10]
	}
	lst := append([]RecentBatchStoreEntry{e}, s.recentStoreByIP[ip]...)
	if len(lst) > 10 {
		lst = lst[:10]
	}
	s.recentStoreByIP[ip] = lst
}

func (s *Network) appendRetrieveEntry(ip string, e RecentBatchRetrieveEntry) {
	s.recentMu.Lock()
	defer s.recentMu.Unlock()
	if s.recentRetrieveByIP == nil {
		s.recentRetrieveByIP = make(map[string][]RecentBatchRetrieveEntry)
	}
	s.recentRetrieveOverall = append([]RecentBatchRetrieveEntry{e}, s.recentRetrieveOverall...)
	if len(s.recentRetrieveOverall) > 10 {
		s.recentRetrieveOverall = s.recentRetrieveOverall[:10]
	}
	lst := append([]RecentBatchRetrieveEntry{e}, s.recentRetrieveByIP[ip]...)
	if len(lst) > 10 {
		lst = lst[:10]
	}
	s.recentRetrieveByIP[ip] = lst
}

// RecentBatchStoreSnapshot returns copies of recent store entries (overall and by IP)
func (s *Network) RecentBatchStoreSnapshot() (overall []RecentBatchStoreEntry, byIP map[string][]RecentBatchStoreEntry) {
	s.recentMu.Lock()
	defer s.recentMu.Unlock()
	overall = append([]RecentBatchStoreEntry(nil), s.recentStoreOverall...)
	byIP = make(map[string][]RecentBatchStoreEntry, len(s.recentStoreByIP))
	for k, v := range s.recentStoreByIP {
		byIP[k] = append([]RecentBatchStoreEntry(nil), v...)
	}
	return
}

// RecentBatchRetrieveSnapshot returns copies of recent retrieve entries (overall and by IP)
func (s *Network) RecentBatchRetrieveSnapshot() (overall []RecentBatchRetrieveEntry, byIP map[string][]RecentBatchRetrieveEntry) {
	s.recentMu.Lock()
	defer s.recentMu.Unlock()
	overall = append([]RecentBatchRetrieveEntry(nil), s.recentRetrieveOverall...)
	byIP = make(map[string][]RecentBatchRetrieveEntry, len(s.recentRetrieveByIP))
	for k, v := range s.recentRetrieveByIP {
		byIP[k] = append([]RecentBatchRetrieveEntry(nil), v...)
	}
	return
}

// helper to avoid unused import warning if needed
var _ = time.Now
var _ = sync.Mutex{}
