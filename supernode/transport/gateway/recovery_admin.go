package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	sntypes "github.com/LumeraProtocol/lumera/x/supernode/v1/types"
	"github.com/LumeraProtocol/supernode/v2/p2p"
	"github.com/LumeraProtocol/supernode/v2/pkg/cascadekit"
	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
	cascadeService "github.com/LumeraProtocol/supernode/v2/supernode/cascade"
)

const (
	recoveryBasePath    = "/api/v1/recovery"
	recoveryHeaderToken = "X-Lumera-Recovery-Token"
	recoveryInternal    = "/api/v1/recovery/internal/probe"
	recoveryWriteWindow = 5 * time.Minute
)

// RecoveryAdminToken authorizes recovery endpoints.
// Set via ldflags:
// -ldflags "-X github.com/LumeraProtocol/supernode/v2/supernode/transport/gateway.RecoveryAdminToken=<token>"
var RecoveryAdminToken string

type recoveryAdmin struct {
	enabled       bool
	selfSupernode string
	gatewayPort   int

	cascadeFactory cascadeService.CascadeServiceFactory
	p2pClient      p2p.Client

	httpClient *http.Client

	reseedSem chan struct{}
	statusSem chan struct{}
}

type reseedReq struct {
	ActionID  string `json:"action_id"`
	Signature string `json:"signature,omitempty"`
}

type internalProbeReq struct {
	ActionID   string   `json:"action_id"`
	IndexIDs   []string `json:"index_ids"`
	LayoutIDs  []string `json:"layout_ids"`
	SymbolKeys []string `json:"symbol_keys"`
}

type internalProbeResp struct {
	OK                 bool   `json:"ok"`
	ActionID           string `json:"action_id"`
	CheckedSupernode   string `json:"checked_supernode"`
	IndexFilesPresent  int    `json:"index_files_present"`
	LayoutFilesPresent int    `json:"layout_files_present"`
	SymbolsPresent     int    `json:"symbols_present"`
	Error              string `json:"error,omitempty"`
}

type artefactBundle struct {
	ActionID     string
	IndexIDs     []string
	LayoutIDs    []string
	SymbolKeys   []string
	IndexPayload [][]byte
	LayoutRaw    [][]byte
}

type supernodeTarget struct {
	SupernodeAddress string `json:"supernode_address"`
	State            string `json:"state"`
	Service          string `json:"service"`
	Host             string `json:"host"`
	GatewayBase      string `json:"gateway_base"`
	Active           bool   `json:"active"`
}

func newRecoveryAdminFromEnv(deps *RecoveryDeps, gatewayPort int) *recoveryAdmin {
	if deps == nil {
		return nil
	}
	return &recoveryAdmin{
		enabled:        true,
		selfSupernode:  strings.TrimSpace(deps.SelfSupernodeAddress),
		gatewayPort:    gatewayPort,
		cascadeFactory: deps.CascadeFactory,
		p2pClient:      deps.P2PClient,
		httpClient: &http.Client{
			Timeout: 45 * time.Second,
		},
		reseedSem: make(chan struct{}, 1),
		statusSem: make(chan struct{}, 1),
	}
}

func (ra *recoveryAdmin) register(mux *http.ServeMux) {
	if ra == nil || !ra.enabled {
		return
	}
	mux.HandleFunc(recoveryBasePath+"/health", ra.wrap(ra.handleHealth))
	mux.HandleFunc(recoveryBasePath+"/reseed", ra.wrap(ra.handleReseed))
	mux.HandleFunc(recoveryBasePath+"/actions/", ra.wrap(ra.handleActionRoutes))
	mux.HandleFunc(recoveryInternal, ra.wrap(ra.handleInternalProbe))
}

func (ra *recoveryAdmin) wrap(next func(http.ResponseWriter, *http.Request)) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		if ra == nil || !ra.enabled {
			http.NotFound(w, r)
			return
		}
		tok := strings.TrimSpace(RecoveryAdminToken)
		if tok == "" {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false, "error": "recovery token not configured"})
			return
		}
		if strings.TrimSpace(r.Header.Get(recoveryHeaderToken)) != tok {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"ok": false, "error": "unauthorized"})
			return
		}
		next(w, r)
	}
}

func (ra *recoveryAdmin) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "method not allowed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":             true,
		"self_supernode": ra.selfSupernode,
		"gateway_port":   ra.gatewayPort,
	})
}

func (ra *recoveryAdmin) handleActionRoutes(w http.ResponseWriter, r *http.Request) {
	// Public route: /api/v1/recovery/actions/{action_id}/status
	const prefix = recoveryBasePath + "/actions/"
	p := strings.TrimPrefix(r.URL.Path, prefix)
	if p == r.URL.Path || !strings.HasSuffix(p, "/status") {
		http.NotFound(w, r)
		return
	}
	actionID := strings.TrimSuffix(p, "/status")
	actionID = strings.Trim(actionID, "/")
	if actionID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "missing action_id"})
		return
	}
	ra.handleStatus(w, r, actionID)
}

func (ra *recoveryAdmin) handleReseed(w http.ResponseWriter, r *http.Request) {
	ra.extendWriteDeadline(w, recoveryWriteWindow)

	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "method not allowed"})
		return
	}
	if ra.cascadeFactory == nil || ra.p2pClient == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false, "error": "reseed dependencies not configured"})
		return
	}

	var req reseedReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid json"})
		return
	}
	req.ActionID = strings.TrimSpace(req.ActionID)
	if req.ActionID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "missing action_id"})
		return
	}

	select {
	case ra.reseedSem <- struct{}{}:
		defer func() { <-ra.reseedSem }()
	default:
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"ok": false, "error": "reseed already running"})
		return
	}

	start := time.Now()
	ctx := logtrace.CtxWithCorrelationID(r.Context(), req.ActionID)
	ctx = logtrace.CtxWithOrigin(ctx, "recovery_reseed")

	task := ra.cascadeFactory.NewCascadeRegistrationTask()
	crt, ok := task.(*cascadeService.CascadeRegistrationTask)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "unexpected task type"})
		return
	}

	// Recovery reseed path: decode -> re-encode -> regenerate artefacts -> store via register flow.
	reseedRes, err := crt.RecoveryReseed(ctx, &cascadeService.RecoveryReseedRequest{
		ActionID:  req.ActionID,
		Signature: req.Signature,
	})
	if err != nil {
		var events int
		var lastEvent string
		var cleanupErr string
		if reseedRes != nil {
			events = reseedRes.DownloadEvents
			lastEvent = reseedRes.DownloadLastEvent
			cleanupErr = reseedRes.DecodeCleanupError
		}
		resp := map[string]any{
			"ok":                          false,
			"action_id":                   req.ActionID,
			"downloadable":                false,
			"downloadable_from_supernode": "",
			"checked_supernode":           ra.selfSupernode,
			"events":                      events,
			"last_event":                  lastEvent,
			"duration_ms":                 time.Since(start).Milliseconds(),
			"error":                       err.Error(),
			"reseed_mode":                 "reencode_register_flow",
		}
		if cleanupErr != "" {
			resp["decode_cleanup_error"] = cleanupErr
		}
		writeJSON(w, http.StatusOK, resp)
		return
	}
	if reseedRes == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":        false,
			"action_id": req.ActionID,
			"error":     "empty reseed result",
		})
		return
	}

	bundle := artefactBundle{
		ActionID:   req.ActionID,
		IndexIDs:   reseedRes.IndexIDs,
		LayoutIDs:  reseedRes.LayoutIDs,
		SymbolKeys: reseedRes.SymbolKeys,
	}

	// Active set policy for reseed: only active set is considered the replication target set.
	targets, activeTargets, err := ra.listSupernodeTargets(ctx)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "action_id": req.ActionID, "error": fmt.Sprintf("list supernodes: %v", err)})
		return
	}
	if len(activeTargets) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "action_id": req.ActionID, "error": "no active supernodes available"})
		return
	}

	// Probe only active set for post-reseed visibility.
	activeResults := ra.probeTargets(ctx, bundle, activeTargets)
	activeReachable := 0
	for _, pr := range activeResults {
		if pr.OK {
			activeReachable++
		}
	}

	resp := map[string]any{
		"ok":                          true,
		"action_id":                   req.ActionID,
		"downloadable":                true,
		"downloadable_from_supernode": ra.selfSupernode,
		"checked_supernode":           ra.selfSupernode,
		"duration_ms":                 time.Since(start).Milliseconds(),
		"events":                      reseedRes.DownloadEvents,
		"last_event":                  reseedRes.DownloadLastEvent,
		"reseed_mode":                 "reencode_register_flow",
		"artifacts_regenerated":       true,
		"data_hash_verified":          reseedRes.DataHashVerified,
		"rq_params": map[string]any{
			"ic":  reseedRes.RQIC,
			"max": reseedRes.RQMax,
		},
		"index_files_generated":    reseedRes.IndexFilesGenerated,
		"layout_files_generated":   reseedRes.LayoutFilesGenerated,
		"id_files_generated":       reseedRes.IDFilesGenerated,
		"symbols_generated":        reseedRes.SymbolsGenerated,
		"symbols_expected":         len(bundle.SymbolKeys),
		"target_policy":            "active_only",
		"active_targets":           len(activeTargets),
		"active_targets_reachable": activeReachable,
		"all_supernodes_seen":      len(targets),
	}
	if reseedRes.DecodeCleanupError != "" {
		resp["decode_cleanup_error"] = reseedRes.DecodeCleanupError
	}
	writeJSON(w, http.StatusOK, resp)
}

func (ra *recoveryAdmin) handleStatus(w http.ResponseWriter, r *http.Request, actionID string) {
	ra.extendWriteDeadline(w, recoveryWriteWindow)

	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "method not allowed"})
		return
	}
	if ra.cascadeFactory == nil || ra.p2pClient == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false, "error": "status dependencies not configured"})
		return
	}

	select {
	case ra.statusSem <- struct{}{}:
		defer func() { <-ra.statusSem }()
	default:
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"ok": false, "error": "status probe already running"})
		return
	}

	start := time.Now()
	ctx := logtrace.CtxWithCorrelationID(r.Context(), actionID)
	ctx = logtrace.CtxWithOrigin(ctx, "recovery_status")

	// "downloadable" semantics: action can be decoded now via normal download flow (network+local).
	downloadable, downloadErr, downloadEvents, downloadLastEvent, downloadCleanupErr, err := ra.checkDownloadable(ctx, actionID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "action_id": actionID, "error": err.Error()})
		return
	}

	bundle, err := ra.resolveArtefactBundle(ctx, actionID)
	if err != nil {
		resp := map[string]any{
			"ok":                  false,
			"action_id":           actionID,
			"downloadable":        downloadable,
			"checked_supernode":   ra.selfSupernode,
			"download_events":     downloadEvents,
			"download_last_event": downloadLastEvent,
			"duration_ms":         time.Since(start).Milliseconds(),
			"error":               err.Error(),
		}
		if downloadErr != "" {
			resp["download_error"] = downloadErr
		}
		if downloadCleanupErr != "" {
			resp["download_cleanup_error"] = downloadCleanupErr
		}
		writeJSON(w, http.StatusOK, resp)
		return
	}

	// Recovery visibility policy uses all supernodes, irrespective of state.
	allTargets, _, err := ra.listSupernodeTargets(ctx)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "action_id": actionID, "error": fmt.Sprintf("list supernodes: %v", err)})
		return
	}
	if len(allTargets) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "action_id": actionID, "error": "no supernode targets found"})
		return
	}

	results := ra.probeTargets(ctx, bundle, allTargets)

	indexMax := len(bundle.IndexIDs)
	layoutMax := len(bundle.LayoutIDs)
	symbolMax := len(bundle.SymbolKeys)
	probedTotal := len(results)
	reachable := 0
	fullCopyAvailable := false
	fullCopyFrom := ""
	fullCopyFromAll := make([]string, 0, 4)
	excludedEmpty := 0
	dist := make([]map[string]any, 0, len(results))
	for _, pr := range results {
		if pr.OK {
			reachable++
		}
		complete := pr.OK &&
			pr.IndexFilesPresent == indexMax &&
			pr.LayoutFilesPresent == layoutMax &&
			pr.SymbolsPresent == symbolMax
		if complete {
			if !fullCopyAvailable {
				fullCopyFrom = pr.Target.SupernodeAddress
			}
			fullCopyAvailable = true
			fullCopyFromAll = append(fullCopyFromAll, pr.Target.SupernodeAddress)
		}
		// Exclude supernodes that returned no artefact presence at all.
		// This keeps the distribution output focused on nodes with usable recovery signal.
		if pr.IndexFilesPresent == 0 && pr.LayoutFilesPresent == 0 && pr.SymbolsPresent == 0 {
			excludedEmpty++
			continue
		}
		dist = append(dist, map[string]any{
			"supernode_address":    pr.Target.SupernodeAddress,
			"service":              pr.Target.Service,
			"state":                pr.Target.State,
			"reachable":            pr.OK,
			"complete_action":      complete,
			"index_files_present":  pr.IndexFilesPresent,
			"layout_files_present": pr.LayoutFilesPresent,
			"symbols_present":      pr.SymbolsPresent,
			"error":                pr.Error,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":           true,
		"action_id":    actionID,
		"duration_ms":  time.Since(start).Milliseconds(),
		"downloadable": downloadable,
		"downloadable_from_supernode": func() string {
			if downloadable {
				return ra.selfSupernode
			}
			return ""
		}(),
		"downloadable_from_supernodes": func() []string {
			if downloadable {
				return []string{ra.selfSupernode}
			}
			return []string{}
		}(),
		"download_events":        downloadEvents,
		"download_last_event":    downloadLastEvent,
		"download_error":         downloadErr,
		"download_cleanup_error": downloadCleanupErr,
		"full_copy_available":    fullCopyAvailable,
		"full_copy_from_supernode": func() string {
			if fullCopyAvailable {
				return fullCopyFrom
			}
			return ""
		}(),
		"full_copy_from_supernodes": fullCopyFromAll,
		"totals": map[string]any{
			"index_files_total":  indexMax,
			"layout_files_total": layoutMax,
			"symbols_total":      symbolMax,
		},
		"summary": map[string]any{
			"supernodes_checked":        probedTotal,
			"supernodes_reachable":      reachable,
			"supernodes_with_data":      len(dist),
			"supernodes_excluded_empty": excludedEmpty,
			"policy":                    "all_supernodes",
		},
		"distribution": dist,
	})
}

func (ra *recoveryAdmin) checkDownloadable(ctx context.Context, actionID string) (ok bool, downloadErr string, events int, lastEvent string, cleanupErr string, err error) {
	task := ra.cascadeFactory.NewCascadeRegistrationTask()
	crt, castOK := task.(*cascadeService.CascadeRegistrationTask)
	if !castOK {
		return false, "", 0, "", "", fmt.Errorf("unexpected task type")
	}

	var tmpDir string
	dlErr := crt.Download(ctx, &cascadeService.DownloadRequest{
		ActionID:               actionID,
		BypassPrivateSignature: true,
	}, func(resp *cascadeService.DownloadResponse) error {
		events++
		lastEvent = fmt.Sprintf("%d:%s", resp.EventType, resp.Message)
		if resp.EventType == cascadeService.SupernodeEventTypeDecodeCompleted {
			tmpDir = resp.DownloadedDir
		}
		return nil
	})
	if tmpDir != "" {
		if cerr := crt.CleanupDownload(ctx, tmpDir); cerr != nil {
			logtrace.Warn(ctx, "status download cleanup failed", logtrace.Fields{
				logtrace.FieldActionID: actionID,
				logtrace.FieldError:    cerr.Error(),
				"tmp_dir":              tmpDir,
			})
			cleanupErr = cerr.Error()
		}
	}
	if dlErr != nil {
		return false, dlErr.Error(), events, lastEvent, cleanupErr, nil
	}
	return true, "", events, lastEvent, cleanupErr, nil
}

func (ra *recoveryAdmin) handleInternalProbe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "method not allowed"})
		return
	}
	if ra.p2pClient == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false, "error": "p2p not configured"})
		return
	}

	var req internalProbeReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid json"})
		return
	}
	req.ActionID = strings.TrimSpace(req.ActionID)
	if req.ActionID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "missing action_id"})
		return
	}

	resp := ra.localProbe(r.Context(), req.ActionID, req.IndexIDs, req.LayoutIDs, req.SymbolKeys)
	writeJSON(w, http.StatusOK, resp)
}

func (ra *recoveryAdmin) localProbe(ctx context.Context, actionID string, indexIDs, layoutIDs, symbolKeys []string) internalProbeResp {
	out := internalProbeResp{
		OK:               true,
		ActionID:         actionID,
		CheckedSupernode: ra.selfSupernode,
	}

	for _, id := range indexIDs {
		b, err := ra.p2pClient.Retrieve(ctx, id, true)
		if err == nil && len(b) > 0 {
			out.IndexFilesPresent++
		}
	}
	for _, id := range layoutIDs {
		b, err := ra.p2pClient.Retrieve(ctx, id, true)
		if err == nil && len(b) > 0 {
			out.LayoutFilesPresent++
		}
	}
	if len(symbolKeys) > 0 {
		written, err := ra.p2pClient.BatchRetrieveStream(ctx, symbolKeys, int32(len(symbolKeys)), actionID, func(_ string, _ []byte) error {
			return nil
		}, true)
		out.SymbolsPresent = int(written)
		if err != nil && !strings.Contains(strings.ToLower(err.Error()), "local-only") {
			out.OK = false
			out.Error = err.Error()
		}
	}
	return out
}

func (ra *recoveryAdmin) resolveArtefactBundle(ctx context.Context, actionID string) (artefactBundle, error) {
	task := ra.cascadeFactory.NewCascadeRegistrationTask()
	crt, ok := task.(*cascadeService.CascadeRegistrationTask)
	if !ok {
		return artefactBundle{}, fmt.Errorf("unexpected task type")
	}

	act, err := crt.LumeraClient.GetAction(ctx, actionID)
	if err != nil {
		return artefactBundle{}, fmt.Errorf("get action: %w", err)
	}
	meta, err := cascadekit.UnmarshalCascadeMetadata(act.GetAction().Metadata)
	if err != nil {
		return artefactBundle{}, fmt.Errorf("decode cascade metadata: %w", err)
	}

	b := artefactBundle{
		ActionID:     actionID,
		IndexIDs:     dedupeStrings(meta.RqIdsIds),
		IndexPayload: make([][]byte, 0, len(meta.RqIdsIds)),
		LayoutRaw:    make([][]byte, 0),
	}
	if len(b.IndexIDs) == 0 {
		return artefactBundle{}, fmt.Errorf("action has no rq_ids_ids")
	}

	layoutSet := make(map[string]struct{})
	for _, indexID := range b.IndexIDs {
		raw, rerr := ra.p2pClient.Retrieve(ctx, indexID)
		if rerr != nil || len(raw) == 0 {
			return artefactBundle{}, fmt.Errorf("retrieve index_id %s: %v", indexID, rerr)
		}
		b.IndexPayload = append(b.IndexPayload, raw)
		idx, perr := cascadekit.ParseCompressedIndexFile(raw)
		if perr != nil {
			return artefactBundle{}, fmt.Errorf("parse index_id %s: %v", indexID, perr)
		}
		for _, lid := range idx.LayoutIDs {
			lid = strings.TrimSpace(lid)
			if lid == "" {
				continue
			}
			layoutSet[lid] = struct{}{}
		}
	}
	for lid := range layoutSet {
		b.LayoutIDs = append(b.LayoutIDs, lid)
	}
	sort.Strings(b.LayoutIDs)
	if len(b.LayoutIDs) == 0 {
		return artefactBundle{}, fmt.Errorf("no layout ids resolved from index files")
	}

	symbolSet := make(map[string]struct{}, 1024)
	for _, lid := range b.LayoutIDs {
		raw, rerr := ra.p2pClient.Retrieve(ctx, lid)
		if rerr != nil || len(raw) == 0 {
			return artefactBundle{}, fmt.Errorf("retrieve layout_id %s: %v", lid, rerr)
		}
		b.LayoutRaw = append(b.LayoutRaw, raw)
		layout, _, _, perr := cascadekit.ParseRQMetadataFile(raw)
		if perr != nil {
			return artefactBundle{}, fmt.Errorf("parse layout_id %s: %v", lid, perr)
		}
		for _, blk := range layout.Blocks {
			for _, sid := range blk.Symbols {
				sid = strings.TrimSpace(sid)
				if sid == "" {
					continue
				}
				// DHT symbol keys are raw symbol IDs (base58), not path-like block prefixes.
				symbolSet[sid] = struct{}{}
			}
		}
	}
	for k := range symbolSet {
		b.SymbolKeys = append(b.SymbolKeys, k)
	}
	sort.Strings(b.SymbolKeys)
	return b, nil
}

func (ra *recoveryAdmin) listSupernodeTargets(ctx context.Context) ([]supernodeTarget, []supernodeTarget, error) {
	task := ra.cascadeFactory.NewCascadeRegistrationTask()
	crt, ok := task.(*cascadeService.CascadeRegistrationTask)
	if !ok {
		return nil, nil, fmt.Errorf("unexpected task type")
	}
	resp, err := crt.LumeraClient.ListSupernodes(ctx)
	if err != nil {
		return nil, nil, err
	}

	all := make([]supernodeTarget, 0, len(resp.Supernodes))
	active := make([]supernodeTarget, 0, len(resp.Supernodes))
	for _, sn := range resp.Supernodes {
		if sn == nil {
			continue
		}
		service, host := latestServiceAndHost(sn)
		if host == "" {
			continue
		}
		state := latestState(sn)
		t := supernodeTarget{
			SupernodeAddress: strings.TrimSpace(sn.SupernodeAccount),
			State:            state,
			Service:          service,
			Host:             host,
			GatewayBase:      gatewayBaseURL(host, ra.gatewayPort),
			Active:           state == "SUPERNODE_STATE_ACTIVE",
		}
		all = append(all, t)
		if t.Active {
			active = append(active, t)
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].SupernodeAddress < all[j].SupernodeAddress })
	sort.Slice(active, func(i, j int) bool { return active[i].SupernodeAddress < active[j].SupernodeAddress })
	return all, active, nil
}

type probeResult struct {
	Target supernodeTarget
	internalProbeResp
}

func (ra *recoveryAdmin) probeTargets(ctx context.Context, b artefactBundle, targets []supernodeTarget) []probeResult {
	if len(targets) == 0 {
		return nil
	}
	results := make([]probeResult, len(targets))
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	for i := range targets {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			results[i] = ra.probeOne(ctx, targets[i], b)
		}(i)
	}
	wg.Wait()
	return results
}

func (ra *recoveryAdmin) probeOne(ctx context.Context, t supernodeTarget, b artefactBundle) probeResult {
	out := probeResult{Target: t}

	// Self probe avoids HTTP roundtrip.
	if t.SupernodeAddress == ra.selfSupernode || t.Host == "127.0.0.1" || t.Host == "localhost" {
		out.internalProbeResp = ra.localProbe(ctx, b.ActionID, b.IndexIDs, b.LayoutIDs, b.SymbolKeys)
		return out
	}

	reqPayload := internalProbeReq{
		ActionID:   b.ActionID,
		IndexIDs:   b.IndexIDs,
		LayoutIDs:  b.LayoutIDs,
		SymbolKeys: b.SymbolKeys,
	}
	body, _ := json.Marshal(reqPayload)
	url := t.GatewayBase + recoveryInternal
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		out.OK = false
		out.Error = err.Error()
		return out
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(recoveryHeaderToken, strings.TrimSpace(RecoveryAdminToken))
	resp, err := ra.httpClient.Do(req)
	if err != nil {
		out.OK = false
		out.Error = err.Error()
		return out
	}
	defer resp.Body.Close()

	var pr internalProbeResp
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		out.OK = false
		out.Error = fmt.Sprintf("decode response: %v", err)
		return out
	}
	out.internalProbeResp = pr
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		out.OK = false
		if out.Error == "" {
			out.Error = "non-2xx response"
		}
	}
	return out
}

func latestServiceAndHost(sn *sntypes.SuperNode) (service string, host string) {
	if sn == nil || len(sn.PrevIpAddresses) == 0 {
		return "", ""
	}
	var rec *sntypes.IPAddressHistory
	for _, r := range sn.PrevIpAddresses {
		if r == nil {
			continue
		}
		if rec == nil || r.Height > rec.Height {
			rec = r
		}
	}
	if rec == nil {
		return "", ""
	}
	a := strings.TrimSpace(rec.Address)
	if a == "" {
		return "", ""
	}
	a = strings.TrimPrefix(a, "http://")
	a = strings.TrimPrefix(a, "https://")
	if i := strings.Index(a, "/"); i >= 0 {
		a = a[:i]
	}
	host = a
	if h, _, ok := strings.Cut(a, ":"); ok {
		host = h
	}
	if !strings.Contains(a, ":") {
		a += ":4444"
	}
	return a, host
}

func latestState(sn *sntypes.SuperNode) string {
	if sn == nil || len(sn.States) == 0 {
		return ""
	}
	var rec *sntypes.SuperNodeStateRecord
	for _, r := range sn.States {
		if r == nil {
			continue
		}
		if rec == nil || r.Height > rec.Height {
			rec = r
		}
	}
	if rec == nil {
		return ""
	}
	return rec.State.String()
}

func gatewayBaseURL(host string, port int) string {
	h := strings.TrimSpace(host)
	if h == "" {
		return ""
	}
	if strings.Contains(h, ":") && !strings.HasPrefix(h, "[") {
		// IPv6 host
		return "http://[" + h + "]:" + strconv.Itoa(port)
	}
	return "http://" + h + ":" + strconv.Itoa(port)
}

func dedupeStrings(in []string) []string {
	m := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, ok := m[s]; ok {
			continue
		}
		m[s] = struct{}{}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (ra *recoveryAdmin) extendWriteDeadline(w http.ResponseWriter, d time.Duration) {
	if d <= 0 {
		return
	}
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Now().Add(d))
}
