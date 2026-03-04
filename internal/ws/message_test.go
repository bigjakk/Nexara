package ws

import "testing"

func TestValidateChannel(t *testing.T) {
	tests := []struct {
		name  string
		ch    string
		valid bool
	}{
		{"valid metrics", "cluster:550e8400-e29b-41d4-a716-446655440000:metrics", true},
		{"valid alerts", "cluster:550e8400-e29b-41d4-a716-446655440000:alerts", true},
		{"missing prefix", "550e8400-e29b-41d4-a716-446655440000:metrics", false},
		{"wrong prefix", "node:550e8400-e29b-41d4-a716-446655440000:metrics", false},
		{"invalid uuid", "cluster:not-a-uuid:metrics", false},
		{"unknown kind", "cluster:550e8400-e29b-41d4-a716-446655440000:logs", false},
		{"empty", "", false},
		{"uppercase uuid", "cluster:550E8400-E29B-41D4-A716-446655440000:metrics", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ValidateChannel(tt.ch); got != tt.valid {
				t.Errorf("ValidateChannel(%q) = %v, want %v", tt.ch, got, tt.valid)
			}
		})
	}
}

func TestClientChannelToRedis(t *testing.T) {
	tests := []struct {
		name    string
		client  string
		redis   string
		wantErr bool
	}{
		{
			"metrics",
			"cluster:550e8400-e29b-41d4-a716-446655440000:metrics",
			"proxdash:metrics:550e8400-e29b-41d4-a716-446655440000",
			false,
		},
		{
			"alerts",
			"cluster:550e8400-e29b-41d4-a716-446655440000:alerts",
			"proxdash:alerts:550e8400-e29b-41d4-a716-446655440000",
			false,
		},
		{"invalid", "bad:channel", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ClientChannelToRedis(tt.client)
			if (err != nil) != tt.wantErr {
				t.Errorf("ClientChannelToRedis(%q) error = %v, wantErr %v", tt.client, err, tt.wantErr)
				return
			}
			if got != tt.redis {
				t.Errorf("ClientChannelToRedis(%q) = %q, want %q", tt.client, got, tt.redis)
			}
		})
	}
}

func TestRedisChannelToClient(t *testing.T) {
	tests := []struct {
		name    string
		redis   string
		client  string
		wantErr bool
	}{
		{
			"metrics",
			"proxdash:metrics:550e8400-e29b-41d4-a716-446655440000",
			"cluster:550e8400-e29b-41d4-a716-446655440000:metrics",
			false,
		},
		{
			"alerts",
			"proxdash:alerts:550e8400-e29b-41d4-a716-446655440000",
			"cluster:550e8400-e29b-41d4-a716-446655440000:alerts",
			false,
		},
		{"unknown prefix", "other:metrics:uuid", "", true},
		{"unknown kind", "proxdash:logs:uuid", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := RedisChannelToClient(tt.redis)
			if (err != nil) != tt.wantErr {
				t.Errorf("RedisChannelToClient(%q) error = %v, wantErr %v", tt.redis, err, tt.wantErr)
				return
			}
			if got != tt.client {
				t.Errorf("RedisChannelToClient(%q) = %q, want %q", tt.redis, got, tt.client)
			}
		})
	}
}

func TestRoundTrip(t *testing.T) {
	clientCh := "cluster:550e8400-e29b-41d4-a716-446655440000:metrics"

	redisCh, err := ClientChannelToRedis(clientCh)
	if err != nil {
		t.Fatalf("ClientChannelToRedis: %v", err)
	}

	got, err := RedisChannelToClient(redisCh)
	if err != nil {
		t.Fatalf("RedisChannelToClient: %v", err)
	}

	if got != clientCh {
		t.Errorf("round trip: got %q, want %q", got, clientCh)
	}
}
