package config

import "testing"

func envMap(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestDSNFromEnv(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"defaults", map[string]string{}, "postgres://postgres:@localhost:5432/geofence?sslmode=disable"},
		{"parts", map[string]string{
			"DB_HOST": "db", "DB_PORT": "5433", "DB_USER": "geofence", "DB_PASSWORD": "pw", "DB_NAME": "gf", "DB_SSLMODE": "require",
		}, "postgres://geofence:pw@db:5433/gf?sslmode=require"},
		{"password is escaped", map[string]string{"DB_USER": "u", "DB_PASSWORD": "p@ss:w/rd#1"},
			"postgres://u:p%40ss%3Aw%2Frd%231@localhost:5432/geofence?sslmode=disable"},
		{"DATABASE_URL wins", map[string]string{"DATABASE_URL": "postgres://x:y@h/d", "DB_HOST": "ignored"}, "postgres://x:y@h/d"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DSNFromEnv(envMap(tt.env)); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}
