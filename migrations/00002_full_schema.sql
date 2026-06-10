-- +goose Up
CREATE TABLE IF NOT EXISTS accounts (
	id VARCHAR NOT NULL, 
	chatgpt_account_id VARCHAR, 
	email VARCHAR NOT NULL, 
	alias VARCHAR, 
	workspace_id VARCHAR, 
	workspace_label VARCHAR, 
	seat_type VARCHAR, 
	plan_type VARCHAR NOT NULL, 
	routing_policy VARCHAR DEFAULT 'normal' NOT NULL, 
	access_token_encrypted BLOB NOT NULL, 
	refresh_token_encrypted BLOB NOT NULL, 
	id_token_encrypted BLOB NOT NULL, 
	last_refresh DATETIME NOT NULL, 
	created_at DATETIME DEFAULT CURRENT_TIMESTAMP NOT NULL, 
	status VARCHAR(15) NOT NULL, 
	deactivation_reason TEXT, 
	reset_at INTEGER, 
	blocked_at INTEGER, 
	limit_warmup_enabled BOOLEAN DEFAULT 0 NOT NULL, 
	security_work_authorized BOOLEAN DEFAULT 0 NOT NULL, 
	PRIMARY KEY (id)
);
CREATE INDEX IF NOT EXISTS idx_accounts_email ON accounts (email);
CREATE TABLE IF NOT EXISTS proxy_endpoints (
	id VARCHAR NOT NULL, 
	name VARCHAR NOT NULL, 
	scheme VARCHAR NOT NULL, 
	host VARCHAR NOT NULL, 
	port INTEGER NOT NULL, 
	username VARCHAR, 
	password_encrypted BLOB, 
	is_active BOOLEAN DEFAULT 1 NOT NULL, 
	created_at DATETIME DEFAULT CURRENT_TIMESTAMP NOT NULL, 
	updated_at DATETIME DEFAULT CURRENT_TIMESTAMP NOT NULL, 
	PRIMARY KEY (id)
);
CREATE TABLE IF NOT EXISTS proxy_pools (
	id VARCHAR NOT NULL, 
	name VARCHAR NOT NULL, 
	is_active BOOLEAN DEFAULT 1 NOT NULL, 
	created_at DATETIME DEFAULT CURRENT_TIMESTAMP NOT NULL, 
	updated_at DATETIME DEFAULT CURRENT_TIMESTAMP NOT NULL, 
	PRIMARY KEY (id)
);
CREATE TABLE IF NOT EXISTS audit_logs (
	id INTEGER NOT NULL, 
	timestamp DATETIME NOT NULL, 
	action VARCHAR(100) NOT NULL, 
	actor_ip VARCHAR(50), 
	details TEXT, 
	request_id VARCHAR(100), 
	PRIMARY KEY (id)
);
CREATE INDEX IF NOT EXISTS ix_audit_logs_timestamp ON audit_logs (timestamp);
CREATE INDEX IF NOT EXISTS ix_audit_logs_action ON audit_logs (action);
CREATE TABLE IF NOT EXISTS scheduler_leader (
	id INTEGER NOT NULL, 
	leader_id VARCHAR(100) NOT NULL, 
	acquired_at DATETIME NOT NULL, 
	expires_at DATETIME NOT NULL, 
	PRIMARY KEY (id)
);
CREATE INDEX IF NOT EXISTS ix_scheduler_leader_expires_at ON scheduler_leader (expires_at);
CREATE TABLE IF NOT EXISTS api_firewall_allowlist (
	ip_address VARCHAR NOT NULL, 
	created_at DATETIME DEFAULT CURRENT_TIMESTAMP NOT NULL, 
	PRIMARY KEY (ip_address)
);
CREATE TABLE IF NOT EXISTS api_keys (
	id VARCHAR NOT NULL, 
	name VARCHAR NOT NULL, 
	key_hash VARCHAR NOT NULL, 
	key_prefix VARCHAR NOT NULL, 
	allowed_models TEXT, 
	apply_to_codex_model BOOLEAN DEFAULT 0 NOT NULL, 
	enforced_model VARCHAR, 
	enforced_reasoning_effort VARCHAR, 
	enforced_service_tier VARCHAR, 
	traffic_class VARCHAR DEFAULT 'foreground' NOT NULL, 
	account_assignment_scope_enabled BOOLEAN DEFAULT 0 NOT NULL, 
	expires_at DATETIME, 
	is_active BOOLEAN NOT NULL, 
	created_at DATETIME DEFAULT CURRENT_TIMESTAMP NOT NULL, 
	last_used_at DATETIME, 
	PRIMARY KEY (id), 
	UNIQUE (key_hash)
);
CREATE INDEX IF NOT EXISTS idx_api_keys_name ON api_keys (name);
CREATE INDEX IF NOT EXISTS idx_api_keys_hash ON api_keys (key_hash);
CREATE TABLE IF NOT EXISTS rate_limit_attempts (
	id INTEGER NOT NULL, 
	"key" VARCHAR(255) NOT NULL, 
	attempted_at DATETIME NOT NULL, 
	type VARCHAR(50) NOT NULL, 
	PRIMARY KEY (id)
);
CREATE INDEX IF NOT EXISTS ix_rate_limit_attempts_key ON rate_limit_attempts ("key");
CREATE INDEX IF NOT EXISTS ix_rate_limit_attempts_type_key_attempted_at ON rate_limit_attempts (type, "key", attempted_at);
CREATE TABLE IF NOT EXISTS quota_planner_settings (
	id INTEGER NOT NULL, 
	mode VARCHAR DEFAULT 'shadow' NOT NULL, 
	timezone VARCHAR DEFAULT 'UTC' NOT NULL, 
	working_days_json TEXT DEFAULT '[0,1,2,3,4]' NOT NULL, 
	working_hours_start VARCHAR DEFAULT '09:00' NOT NULL, 
	working_hours_end VARCHAR DEFAULT '18:00' NOT NULL, 
	prewarm_enabled BOOLEAN DEFAULT 1 NOT NULL, 
	prewarm_lead_minutes INTEGER DEFAULT 300 NOT NULL, 
	max_warmups_per_day INTEGER DEFAULT 3 NOT NULL, 
	max_warmup_credits_per_day FLOAT DEFAULT (0.0) NOT NULL, 
	min_expected_gain FLOAT DEFAULT (1.0) NOT NULL, 
	forecast_quantile VARCHAR DEFAULT 'p75' NOT NULL, 
	allow_synthetic_traffic BOOLEAN DEFAULT 0 NOT NULL, 
	warmup_model_preference VARCHAR, 
	dry_run BOOLEAN DEFAULT 1 NOT NULL, 
	created_at DATETIME DEFAULT CURRENT_TIMESTAMP NOT NULL, 
	updated_at DATETIME DEFAULT CURRENT_TIMESTAMP NOT NULL, 
	PRIMARY KEY (id)
);
CREATE TABLE IF NOT EXISTS cache_invalidation (
	namespace VARCHAR(50) NOT NULL, 
	version INTEGER DEFAULT '0' NOT NULL, 
	PRIMARY KEY (namespace)
);
CREATE TABLE IF NOT EXISTS bridge_ring_members (
	id VARCHAR(36) NOT NULL, 
	instance_id VARCHAR(255) NOT NULL, 
	registered_at DATETIME NOT NULL, 
	last_heartbeat_at DATETIME NOT NULL, 
	metadata_json TEXT, 
	PRIMARY KEY (id), 
	UNIQUE (instance_id)
);
CREATE TABLE IF NOT EXISTS usage_history (
	id INTEGER NOT NULL, 
	account_id VARCHAR NOT NULL, 
	recorded_at DATETIME DEFAULT CURRENT_TIMESTAMP NOT NULL, 
	window VARCHAR, 
	used_percent FLOAT NOT NULL, 
	input_tokens INTEGER, 
	output_tokens INTEGER, 
	reset_at INTEGER, 
	window_minutes INTEGER, 
	credits_has BOOLEAN, 
	credits_unlimited BOOLEAN, 
	credits_balance FLOAT, 
	PRIMARY KEY (id), 
	FOREIGN KEY(account_id) REFERENCES accounts (id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_usage_recorded_at ON usage_history (recorded_at);
CREATE INDEX IF NOT EXISTS idx_usage_window_account_time ON usage_history (coalesce(window, 'primary'), account_id, recorded_at);
CREATE INDEX IF NOT EXISTS idx_usage_window_raw_account_latest ON usage_history (window, account_id, recorded_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_usage_account_time ON usage_history (account_id, recorded_at);
CREATE INDEX IF NOT EXISTS idx_usage_window_account_latest ON usage_history (coalesce(window, 'primary'), account_id, recorded_at DESC, id DESC);
CREATE TABLE IF NOT EXISTS additional_usage_history (
	id INTEGER NOT NULL, 
	account_id VARCHAR NOT NULL, 
	quota_key VARCHAR NOT NULL, 
	limit_name VARCHAR NOT NULL, 
	metered_feature VARCHAR NOT NULL, 
	window VARCHAR NOT NULL, 
	used_percent FLOAT NOT NULL, 
	reset_at INTEGER, 
	window_minutes INTEGER, 
	recorded_at DATETIME DEFAULT CURRENT_TIMESTAMP NOT NULL, 
	PRIMARY KEY (id), 
	FOREIGN KEY(account_id) REFERENCES accounts (id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS ix_additional_usage_quota_window ON additional_usage_history (quota_key, window, account_id, recorded_at);
CREATE INDEX IF NOT EXISTS ix_additional_usage_history_composite ON additional_usage_history (account_id, quota_key, window, recorded_at);
CREATE INDEX IF NOT EXISTS ix_additional_usage_history_recorded_at ON additional_usage_history (recorded_at);
CREATE INDEX IF NOT EXISTS ix_additional_usage_history_account_id ON additional_usage_history (account_id);
CREATE TABLE IF NOT EXISTS request_logs (
	id INTEGER NOT NULL, 
	account_id VARCHAR, 
	api_key_id VARCHAR, 
	session_id VARCHAR, 
	request_id VARCHAR NOT NULL, 
	request_kind VARCHAR DEFAULT 'normal' NOT NULL, 
	requested_at DATETIME DEFAULT CURRENT_TIMESTAMP NOT NULL, 
	deleted_at DATETIME, 
	model VARCHAR NOT NULL, 
	plan_type VARCHAR, 
	source VARCHAR, 
	useragent TEXT, 
	useragent_group VARCHAR, 
	transport VARCHAR, 
	service_tier VARCHAR, 
	requested_service_tier VARCHAR, 
	actual_service_tier VARCHAR, 
	input_tokens INTEGER, 
	output_tokens INTEGER, 
	cached_input_tokens INTEGER, 
	reasoning_tokens INTEGER, 
	cost_usd FLOAT, 
	reasoning_effort VARCHAR, 
	latency_ms INTEGER, 
	latency_first_token_ms INTEGER, 
	status VARCHAR NOT NULL, 
	error_code VARCHAR, 
	error_message TEXT, 
	failure_phase VARCHAR, 
	failure_detail TEXT, 
	failure_exception_type VARCHAR, 
	upstream_status_code INTEGER, 
	upstream_error_code VARCHAR, 
	bridge_stage VARCHAR, 
	upstream_proxy_route_mode VARCHAR, 
	upstream_proxy_pool_id VARCHAR, 
	upstream_proxy_endpoint_id VARCHAR, 
	upstream_proxy_fallback_used BOOLEAN, 
	upstream_proxy_fail_closed_reason VARCHAR, 
	PRIMARY KEY (id), 
	FOREIGN KEY(account_id) REFERENCES accounts (id) ON DELETE SET NULL
);
CREATE INDEX IF NOT EXISTS idx_logs_request_status_api_key_time ON request_logs (request_id, status, api_key_id, requested_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_logs_request_kind_time ON request_logs (request_kind, requested_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_logs_source_requested_at ON request_logs (source, requested_at DESC);
CREATE INDEX IF NOT EXISTS idx_logs_deleted_at_requested_at_id ON request_logs (deleted_at, requested_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_logs_requested_at_model_tier ON request_logs (requested_at DESC, model, service_tier);
CREATE INDEX IF NOT EXISTS idx_logs_api_key_time_account ON request_logs (api_key_id, requested_at DESC, account_id);
CREATE INDEX IF NOT EXISTS idx_logs_model_effort_time ON request_logs (model, reasoning_effort, requested_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_logs_status_error_time ON request_logs (status, error_code, requested_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_logs_useragent_group ON request_logs (useragent_group);
CREATE INDEX IF NOT EXISTS idx_logs_requested_at ON request_logs (requested_at);
CREATE INDEX IF NOT EXISTS idx_logs_requested_at_id ON request_logs (requested_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_logs_request_status_api_key_session_time ON request_logs (request_id, status, api_key_id, session_id, requested_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_logs_account_time ON request_logs (account_id, requested_at);
CREATE INDEX IF NOT EXISTS idx_logs_api_key_time ON request_logs (api_key_id, requested_at DESC, id DESC);
CREATE TABLE IF NOT EXISTS proxy_pool_members (
	id VARCHAR NOT NULL, 
	pool_id VARCHAR NOT NULL, 
	endpoint_id VARCHAR NOT NULL, 
	sort_order INTEGER DEFAULT 0 NOT NULL, 
	weight INTEGER DEFAULT 1 NOT NULL, 
	is_active BOOLEAN DEFAULT 1 NOT NULL, 
	created_at DATETIME DEFAULT CURRENT_TIMESTAMP NOT NULL, 
	PRIMARY KEY (id), 
	CONSTRAINT uq_proxy_pool_members_pool_endpoint UNIQUE (pool_id, endpoint_id), 
	FOREIGN KEY(pool_id) REFERENCES proxy_pools (id) ON DELETE CASCADE, 
	FOREIGN KEY(endpoint_id) REFERENCES proxy_endpoints (id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_proxy_pool_members_pool_order ON proxy_pool_members (pool_id, is_active, sort_order, id);
CREATE TABLE IF NOT EXISTS account_proxy_bindings (
	id VARCHAR NOT NULL, 
	account_id VARCHAR NOT NULL, 
	pool_id VARCHAR NOT NULL, 
	is_active BOOLEAN DEFAULT 1 NOT NULL, 
	created_at DATETIME DEFAULT CURRENT_TIMESTAMP NOT NULL, 
	updated_at DATETIME DEFAULT CURRENT_TIMESTAMP NOT NULL, 
	PRIMARY KEY (id), 
	CONSTRAINT uq_account_proxy_bindings_account UNIQUE (account_id), 
	FOREIGN KEY(account_id) REFERENCES accounts (id) ON DELETE CASCADE, 
	FOREIGN KEY(pool_id) REFERENCES proxy_pools (id) ON DELETE RESTRICT
);
CREATE TABLE IF NOT EXISTS account_limit_warmups (
	id INTEGER NOT NULL, 
	account_id VARCHAR NOT NULL, 
	window VARCHAR NOT NULL, 
	reset_at INTEGER NOT NULL, 
	status VARCHAR NOT NULL, 
	model VARCHAR NOT NULL, 
	attempted_at DATETIME NOT NULL, 
	completed_at DATETIME, 
	error_code VARCHAR, 
	error_message TEXT, 
	created_at DATETIME DEFAULT CURRENT_TIMESTAMP NOT NULL, 
	updated_at DATETIME DEFAULT CURRENT_TIMESTAMP NOT NULL, 
	PRIMARY KEY (id), 
	CONSTRAINT uq_account_limit_warmups_account_window_reset UNIQUE (account_id, window, reset_at), 
	FOREIGN KEY(account_id) REFERENCES accounts (id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_account_limit_warmups_account_attempted ON account_limit_warmups (account_id, attempted_at DESC);
CREATE INDEX IF NOT EXISTS idx_account_limit_warmups_status_attempted ON account_limit_warmups (status, attempted_at DESC);
CREATE TABLE IF NOT EXISTS sticky_sessions (
	"key" VARCHAR NOT NULL, 
	kind VARCHAR(13) DEFAULT 'sticky_thread' NOT NULL, 
	account_id VARCHAR NOT NULL, 
	created_at DATETIME DEFAULT CURRENT_TIMESTAMP NOT NULL, 
	updated_at DATETIME DEFAULT CURRENT_TIMESTAMP NOT NULL, 
	PRIMARY KEY ("key", kind), 
	FOREIGN KEY(account_id) REFERENCES accounts (id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_sticky_kind_updated_at ON sticky_sessions (kind, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_sticky_account ON sticky_sessions (account_id);
CREATE TABLE IF NOT EXISTS dashboard_settings (
	id INTEGER NOT NULL, 
	sticky_threads_enabled BOOLEAN DEFAULT 1 NOT NULL, 
	upstream_stream_transport VARCHAR DEFAULT 'default' NOT NULL, 
	prefer_earlier_reset_accounts BOOLEAN DEFAULT 1 NOT NULL, 
	prefer_earlier_reset_window VARCHAR DEFAULT 'secondary' NOT NULL, 
	routing_strategy VARCHAR DEFAULT 'capacity_weighted' NOT NULL, 
	relative_availability_power FLOAT DEFAULT (2.0) NOT NULL, 
	relative_availability_top_k INTEGER DEFAULT 5 NOT NULL, 
	single_account_id VARCHAR, 
	openai_cache_affinity_max_age_seconds INTEGER DEFAULT 1800 NOT NULL, 
	dashboard_session_ttl_seconds INTEGER DEFAULT 43200 NOT NULL, 
	import_without_overwrite BOOLEAN DEFAULT 1 NOT NULL, 
	totp_required_on_login BOOLEAN NOT NULL, 
	password_hash TEXT, 
	bootstrap_token_encrypted BLOB, 
	bootstrap_token_hash BLOB, 
	api_key_auth_enabled BOOLEAN NOT NULL, 
	totp_secret_encrypted BLOB, 
	totp_last_verified_step INTEGER, 
	http_responses_session_bridge_prompt_cache_idle_ttl_seconds INTEGER DEFAULT 3600 NOT NULL, 
	http_responses_session_bridge_gateway_safe_mode BOOLEAN DEFAULT 0 NOT NULL, 
	upstream_proxy_routing_enabled BOOLEAN DEFAULT 0 NOT NULL, 
	upstream_proxy_default_pool_id VARCHAR, 
	sticky_reallocation_budget_threshold_pct FLOAT DEFAULT (95.0) NOT NULL, 
	sticky_reallocation_primary_budget_threshold_pct FLOAT DEFAULT (95.0) NOT NULL, 
	sticky_reallocation_secondary_budget_threshold_pct FLOAT DEFAULT (100.0) NOT NULL, 
	additional_quota_routing_policies_json TEXT DEFAULT '{}' NOT NULL, 
	limit_warmup_enabled BOOLEAN DEFAULT 0 NOT NULL, 
	limit_warmup_windows VARCHAR DEFAULT 'both' NOT NULL, 
	limit_warmup_model VARCHAR DEFAULT 'auto' NOT NULL, 
	limit_warmup_prompt TEXT DEFAULT 'Say OK.' NOT NULL, 
	limit_warmup_cooldown_seconds INTEGER DEFAULT 3600 NOT NULL, 
	limit_warmup_min_available_percent FLOAT DEFAULT (100.0) NOT NULL, 
	weekly_pace_working_days VARCHAR DEFAULT '0,1,2,3,4,5,6' NOT NULL, 
	warmup_model VARCHAR DEFAULT 'gpt-5.4-mini' NOT NULL, 
	created_at DATETIME DEFAULT CURRENT_TIMESTAMP NOT NULL, 
	updated_at DATETIME DEFAULT CURRENT_TIMESTAMP NOT NULL, 
	PRIMARY KEY (id), 
	FOREIGN KEY(upstream_proxy_default_pool_id) REFERENCES proxy_pools (id) ON DELETE SET NULL
);
CREATE TABLE IF NOT EXISTS api_key_accounts (
	api_key_id VARCHAR NOT NULL, 
	account_id VARCHAR NOT NULL, 
	created_at DATETIME DEFAULT CURRENT_TIMESTAMP NOT NULL, 
	PRIMARY KEY (api_key_id, account_id), 
	FOREIGN KEY(api_key_id) REFERENCES api_keys (id) ON DELETE CASCADE, 
	FOREIGN KEY(account_id) REFERENCES accounts (id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_api_key_accounts_account_id ON api_key_accounts (account_id);
CREATE TABLE IF NOT EXISTS api_key_limits (
	id INTEGER NOT NULL, 
	api_key_id VARCHAR NOT NULL, 
	limit_type VARCHAR(13) NOT NULL, 
	limit_window VARCHAR(7) NOT NULL, 
	max_value BIGINT NOT NULL, 
	current_value BIGINT DEFAULT 0 NOT NULL, 
	model_filter VARCHAR, 
	reset_at DATETIME NOT NULL, 
	PRIMARY KEY (id), 
	FOREIGN KEY(api_key_id) REFERENCES api_keys (id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_api_key_limits_reset_at ON api_key_limits (reset_at);
CREATE INDEX IF NOT EXISTS idx_api_key_limits_key_id ON api_key_limits (api_key_id);
CREATE TABLE IF NOT EXISTS api_key_usage_reservations (
	id VARCHAR NOT NULL, 
	api_key_id VARCHAR NOT NULL, 
	model VARCHAR NOT NULL, 
	status VARCHAR NOT NULL, 
	input_tokens BIGINT, 
	output_tokens BIGINT, 
	cached_input_tokens BIGINT, 
	cost_microdollars BIGINT, 
	created_at DATETIME DEFAULT CURRENT_TIMESTAMP NOT NULL, 
	updated_at DATETIME DEFAULT CURRENT_TIMESTAMP NOT NULL, 
	PRIMARY KEY (id), 
	FOREIGN KEY(api_key_id) REFERENCES api_keys (id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_api_key_usage_reservations_status ON api_key_usage_reservations (status);
CREATE INDEX IF NOT EXISTS idx_api_key_usage_reservations_key_id ON api_key_usage_reservations (api_key_id);
CREATE INDEX IF NOT EXISTS idx_api_key_usage_reservations_status_updated_at ON api_key_usage_reservations (status, updated_at);
CREATE TABLE IF NOT EXISTS quota_planner_decisions (
	id VARCHAR(36) NOT NULL, 
	created_at DATETIME DEFAULT CURRENT_TIMESTAMP NOT NULL, 
	mode VARCHAR NOT NULL, 
	account_id VARCHAR, 
	action VARCHAR NOT NULL, 
	scheduled_at DATETIME, 
	executed_at DATETIME, 
	score FLOAT DEFAULT (0.0) NOT NULL, 
	reason TEXT, 
	forecast_snapshot_hash VARCHAR(64), 
	state_before_json TEXT, 
	state_after_json TEXT, 
	status VARCHAR DEFAULT 'planned' NOT NULL, 
	idempotency_key VARCHAR NOT NULL, 
	PRIMARY KEY (id), 
	FOREIGN KEY(account_id) REFERENCES accounts (id) ON DELETE SET NULL, 
	UNIQUE (idempotency_key)
);
CREATE INDEX IF NOT EXISTS idx_quota_planner_decisions_status_created ON quota_planner_decisions (status, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_quota_planner_decisions_account_created ON quota_planner_decisions (account_id, created_at DESC);
CREATE TABLE IF NOT EXISTS quota_window_observations (
	id INTEGER NOT NULL, 
	account_id VARCHAR NOT NULL, 
	observed_at DATETIME DEFAULT CURRENT_TIMESTAMP NOT NULL, 
	model VARCHAR, 
	primary_remaining_percent FLOAT, 
	primary_reset_at INTEGER, 
	secondary_remaining_percent FLOAT, 
	secondary_reset_at INTEGER, 
	source VARCHAR NOT NULL, 
	confidence VARCHAR DEFAULT 'unknown' NOT NULL, 
	PRIMARY KEY (id), 
	FOREIGN KEY(account_id) REFERENCES accounts (id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_quota_window_observations_account_time ON quota_window_observations (account_id, observed_at DESC);
CREATE TABLE IF NOT EXISTS http_bridge_sessions (
	id VARCHAR(36) NOT NULL, 
	session_key_kind VARCHAR(64) NOT NULL, 
	session_key_value TEXT NOT NULL, 
	session_key_hash VARCHAR(64) NOT NULL, 
	api_key_scope VARCHAR(255) NOT NULL, 
	owner_instance_id VARCHAR(255), 
	owner_epoch INTEGER DEFAULT 0 NOT NULL, 
	lease_expires_at DATETIME, 
	state VARCHAR(8) DEFAULT 'active' NOT NULL, 
	account_id VARCHAR, 
	model VARCHAR, 
	service_tier VARCHAR, 
	latest_turn_state TEXT, 
	latest_response_id TEXT, 
	latest_input_item_count INTEGER, 
	latest_input_full_fingerprint VARCHAR(64), 
	created_at DATETIME DEFAULT CURRENT_TIMESTAMP NOT NULL, 
	updated_at DATETIME DEFAULT CURRENT_TIMESTAMP NOT NULL, 
	last_seen_at DATETIME DEFAULT CURRENT_TIMESTAMP NOT NULL, 
	closed_at DATETIME, 
	PRIMARY KEY (id), 
	CONSTRAINT uq_http_bridge_sessions_session_key UNIQUE (session_key_kind, session_key_hash, api_key_scope), 
	FOREIGN KEY(account_id) REFERENCES accounts (id) ON DELETE SET NULL
);
CREATE INDEX IF NOT EXISTS idx_http_bridge_sessions_lease ON http_bridge_sessions (lease_expires_at);
CREATE INDEX IF NOT EXISTS idx_http_bridge_sessions_owner_state ON http_bridge_sessions (owner_instance_id, state);
CREATE INDEX IF NOT EXISTS idx_http_bridge_sessions_last_seen ON http_bridge_sessions (last_seen_at DESC);
CREATE INDEX IF NOT EXISTS idx_http_bridge_sessions_latest_turn_scope_state_seen ON http_bridge_sessions (latest_turn_state, api_key_scope, state, last_seen_at DESC, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_http_bridge_sessions_latest_response_scope_state_seen ON http_bridge_sessions (latest_response_id, api_key_scope, state, last_seen_at DESC, updated_at DESC);
CREATE TABLE IF NOT EXISTS api_key_usage_reservation_items (
	id INTEGER NOT NULL, 
	reservation_id VARCHAR NOT NULL, 
	limit_id INTEGER NOT NULL, 
	limit_type VARCHAR NOT NULL, 
	reserved_delta BIGINT NOT NULL, 
	actual_delta BIGINT, 
	expected_reset_at DATETIME NOT NULL, 
	created_at DATETIME DEFAULT CURRENT_TIMESTAMP NOT NULL, 
	updated_at DATETIME DEFAULT CURRENT_TIMESTAMP NOT NULL, 
	PRIMARY KEY (id), 
	CONSTRAINT uq_reservation_limit UNIQUE (reservation_id, limit_id), 
	FOREIGN KEY(reservation_id) REFERENCES api_key_usage_reservations (id) ON DELETE CASCADE, 
	FOREIGN KEY(limit_id) REFERENCES api_key_limits (id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_api_key_usage_res_items_reservation_id ON api_key_usage_reservation_items (reservation_id);
CREATE TABLE IF NOT EXISTS http_bridge_session_aliases (
	id VARCHAR(36) NOT NULL, 
	session_id VARCHAR(36) NOT NULL, 
	alias_kind VARCHAR(64) NOT NULL, 
	alias_value TEXT NOT NULL, 
	alias_hash VARCHAR(64) NOT NULL, 
	api_key_scope VARCHAR(255) NOT NULL, 
	created_at DATETIME DEFAULT CURRENT_TIMESTAMP NOT NULL, 
	updated_at DATETIME DEFAULT CURRENT_TIMESTAMP NOT NULL, 
	PRIMARY KEY (id), 
	CONSTRAINT uq_http_bridge_session_aliases_alias UNIQUE (alias_kind, alias_hash, api_key_scope), 
	FOREIGN KEY(session_id) REFERENCES http_bridge_sessions (id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_http_bridge_session_aliases_alias_kind_hash_scope ON http_bridge_session_aliases (alias_kind, alias_hash, api_key_scope);
CREATE INDEX IF NOT EXISTS idx_http_bridge_session_aliases_session_id ON http_bridge_session_aliases (session_id);

-- +goose Down
DROP TABLE IF EXISTS http_bridge_session_aliases;
DROP TABLE IF EXISTS api_key_usage_reservation_items;
DROP TABLE IF EXISTS http_bridge_sessions;
DROP TABLE IF EXISTS quota_window_observations;
DROP TABLE IF EXISTS quota_planner_decisions;
DROP TABLE IF EXISTS api_key_usage_reservations;
DROP TABLE IF EXISTS api_key_limits;
DROP TABLE IF EXISTS api_key_accounts;
DROP TABLE IF EXISTS dashboard_settings;
DROP TABLE IF EXISTS sticky_sessions;
DROP TABLE IF EXISTS account_limit_warmups;
DROP TABLE IF EXISTS account_proxy_bindings;
DROP TABLE IF EXISTS proxy_pool_members;
DROP TABLE IF EXISTS request_logs;
DROP TABLE IF EXISTS additional_usage_history;
DROP TABLE IF EXISTS usage_history;
DROP TABLE IF EXISTS bridge_ring_members;
DROP TABLE IF EXISTS cache_invalidation;
DROP TABLE IF EXISTS quota_planner_settings;
DROP TABLE IF EXISTS rate_limit_attempts;
DROP TABLE IF EXISTS api_keys;
DROP TABLE IF EXISTS api_firewall_allowlist;
DROP TABLE IF EXISTS scheduler_leader;
DROP TABLE IF EXISTS audit_logs;
DROP TABLE IF EXISTS proxy_pools;
DROP TABLE IF EXISTS proxy_endpoints;
DROP TABLE IF EXISTS accounts;
