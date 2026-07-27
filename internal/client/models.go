package client

import "time"

// The structs below mirror lean-api's read DTOs. They deliberately carry only
// the fields leanctl renders in tables — `-o json` prints the server's own
// bytes, so an unmapped field is never lost, only unformatted.

// Me is GET /auth/me.
type Me struct {
	Email           string `json:"email"`
	DisplayName     string `json:"display_name"`
	Role            string `json:"role"`
	TenantName      string `json:"tenant_name"`
	TenantShortName string `json:"tenant_short_name"`
	Error           string `json:"error"`
	ErrorMsg        string `json:"error_msg"`
}

// Demand is a demand record.
type Demand struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	Description    string    `json:"description"`
	CreatedByEmail string    `json:"created_by_email"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// Dashboard is a dashboard record (the Perses spec lives in Data).
type Dashboard struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	DemandID    string    `json:"demand_id"`
	Version     int       `json:"version"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Agent is an edge collector record.
type Agent struct {
	ID                     string     `json:"id"`
	Name                   string     `json:"name"`
	Status                 string     `json:"status"`
	Version                string     `json:"version"`
	AgentKey               *string    `json:"agent_key,omitempty"`
	TimeseriesCollectedAVM int        `json:"timeseries_collected_avm"`
	TimeseriesCollectedDP  int        `json:"timeseries_collected_dpvm"`
	LastTimeSeen           *time.Time `json:"last_time_seen"`
	DemandLastUpdate       *time.Time `json:"demand_last_update"`
	CreatedAt              time.Time  `json:"created_at"`
}

// AlertRule is an alert rule record.
type AlertRule struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Query       string     `json:"query"`
	ConditionOp string     `json:"condition_op"`
	Threshold   float64    `json:"threshold"`
	Severity    string     `json:"severity"`
	Status      string     `json:"status"`
	Paused      bool       `json:"paused"`
	Interval    string     `json:"interval"`
	For         string     `json:"for"`
	DemandID    string     `json:"demand_id"`
	ChannelIDs  []string   `json:"channel_ids"`
	MutedUntil  *time.Time `json:"muted_until"`
	LastEvalAt  *time.Time `json:"last_eval_at"`
	LastFiredAt *time.Time `json:"last_fired_at"`
}

// NotificationChannel is a delivery target for alerts.
type NotificationChannel struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	Type           string    `json:"type"`
	Enabled        bool      `json:"enabled"`
	CreatedByEmail string    `json:"created_by_email"`
	CreatedAt      time.Time `json:"created_at"`
}

// SyntheticCheck is a scheduled HTTP probe.
type SyntheticCheck struct {
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	Method        string     `json:"method"`
	URL           string     `json:"url"`
	Status        string     `json:"status"`
	Paused        bool       `json:"paused"`
	Interval      string     `json:"interval"`
	LastResultOK  *bool      `json:"last_result_ok"`
	LastStatus    int        `json:"last_status_code"`
	LastLatencyMS int        `json:"last_latency_ms"`
	LastRunAt     *time.Time `json:"last_run_at"`
	LastError     string     `json:"last_error"`
	DemandID      string     `json:"demand_id"`
}

// CustomRule is a user-authored subdemand.
type CustomRule struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Kind       string    `json:"kind"`
	Expression string    `json:"expression"`
	Enabled    bool      `json:"enabled"`
	DemandID   string    `json:"demand_id"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// Filter is one materialized demand-set entry.
type Filter struct {
	ID         string    `json:"id"`
	Type       string    `json:"type"`
	Rule       string    `json:"rule"`
	Status     string    `json:"status"`
	ObjectType string    `json:"object_type"`
	ObjectID   string    `json:"object_id"`
	DemandID   string    `json:"demand_id"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// PurgedFilter is one retroactive-purge outcome.
type PurgedFilter struct {
	Rule        string     `json:"rule"`
	Type        string     `json:"type"`
	DemandName  string     `json:"demand_name"`
	Outcome     string     `json:"outcome"`
	PurgedCount int        `json:"purged_count"`
	DeletedAt   *time.Time `json:"deleted_at"`
	ProcessedAt *time.Time `json:"processed_at"`
}

// AuditEntry is one audit-log row.
type AuditEntry struct {
	ID         string    `json:"id"`
	ActorEmail string    `json:"actor_email"`
	Action     string    `json:"action"`
	ObjectType string    `json:"object_type"`
	ObjectName string    `json:"object_name"`
	ObjectID   string    `json:"object_id"`
	CreatedAt  time.Time `json:"created_at"`
}

// Settings is the tenant settings document.
type Settings struct {
	PurgeGrace          string `json:"purge_grace"`
	PurgeGraceDefault   string `json:"purge_grace_default"`
	PurgeGraceIsDefault bool   `json:"purge_grace_is_default"`
}

// StackStatus is the dataplane storage summary.
type StackStatus struct {
	Tenant      string `json:"tenant"`
	DataplaneVM struct {
		Reachable      bool   `json:"reachable"`
		SeriesCount    int64  `json:"series_count"`
		DataSizeBytes  int64  `json:"data_size_bytes"`
		FreeDiskBytes  int64  `json:"free_disk_bytes"`
		TotalDiskBytes int64  `json:"total_disk_bytes"`
		Error          string `json:"error"`
	} `json:"dataplane_vm"`
	Logs struct {
		Reachable   bool   `json:"reachable"`
		VolumeBytes int64  `json:"volume_bytes"`
		WindowDays  int    `json:"window_days"`
		Error       string `json:"error"`
	} `json:"logs"`
	Traces struct {
		Reachable bool   `json:"reachable"`
		SpanCount int64  `json:"span_count"`
		Error     string `json:"error"`
	} `json:"traces"`
}

// APIToken is a personal access token record (never carries the secret,
// except in the one-time create response).
type APIToken struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	TokenPrefix string     `json:"token_prefix"`
	UserEmail   string     `json:"user_email"`
	Role        string     `json:"role"`
	Scopes      []string   `json:"scopes"`
	CreatedAt   time.Time  `json:"created_at"`
	ExpiresAt   *time.Time `json:"expires_at"`
	LastUsedAt  *time.Time `json:"last_used_at"`
	// Token is the plaintext secret, present only in a create response.
	Token string `json:"token"`
}

// SearchResult is the cross-resource search response.
type SearchResult struct {
	Demands              []SearchItem `json:"demands"`
	Dashboards           []SearchItem `json:"dashboards"`
	Agents               []SearchItem `json:"agents"`
	AlertRules           []SearchItem `json:"alert_rules"`
	NotificationChannels []SearchItem `json:"notification_channels"`
}

// SearchItem is one search hit.
type SearchItem struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
