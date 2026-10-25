package config

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// InitConfig reads config.yaml from $HOME/.mfide, /etc/mfide or the working
// directory. MFIDE_* environment variables override the file (MFIDE_DB_HOST, …).
func InitConfig() error {
	return readConfig("config")
}

// NewConfig reads <name>.yaml from the same locations as InitConfig. Tests use it
// to load config_test.yaml.
func NewConfig(name string) error {
	return readConfig(name)
}

func readConfig(name string) error {
	viper.SetConfigName(name)
	viper.SetConfigType("yaml")
	viper.AddConfigPath("$HOME/.mfide")
	viper.AddConfigPath("/etc/mfide")
	viper.AddConfigPath(".")
	viper.AddConfigPath("../..")
	viper.SetEnvPrefix("MFIDE")
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	viper.AutomaticEnv()
	// Defaults so a config without an api section still limits sensibly.
	viper.SetDefault("api.defaultRatePerMin", 1000)
	viper.SetDefault("api.keyCacheTTL", time.Minute)
	viper.SetDefault("api.ipRatePerMin", 60)
	viper.SetDefault("fide.baseUrl", "https://ratings.fide.com/download")
	viper.SetDefault("fide.fetchDelay", 3*time.Second)
	viper.SetDefault("sync.enabled", true)
	viper.SetDefault("sync.at", "03:00")
	viper.SetDefault("retention.currentFiles", 2)
	viper.SetDefault("retention.removedPlayersDays", 90)
	viper.SetDefault("retention.historyMonths", 60)
	viper.SetDefault("log.format", "console")
	viper.SetDefault("log.level", "info")
	return viper.ReadInConfig()
}

// LogFormat is the service log encoding: console or json.
func LogFormat() string {
	return viper.GetString("log.format")
}

// LogLevel is the lowest level the service logs: debug, info, warn or error.
func LogLevel() string {
	return viper.GetString("log.level")
}

// ApiDefaultRatePerMin is the rate limit given to new keys unless overridden.
// It is a fuse against bugs and leaked keys, not a quota.
func ApiDefaultRatePerMin() int {
	return viper.GetInt("api.defaultRatePerMin")
}

// ApiKeyCacheTTL is how long a looked-up key is trusted before it's re-read, so
// a revoke takes effect within this time.
func ApiKeyCacheTTL() time.Duration {
	return viper.GetDuration("api.keyCacheTTL")
}

// ApiIPRatePerMin limits requests without a valid key, per client IP.
func ApiIPRatePerMin() int {
	return viper.GetInt("api.ipRatePerMin")
}

// FideBaseURL is where FIDE's download files live; tests point it elsewhere.
func FideBaseURL() string {
	return viper.GetString("fide.baseUrl")
}

// FideFetchDelay is the pause between consecutive downloads from FIDE.
func FideFetchDelay() time.Duration {
	return viper.GetDuration("fide.fetchDelay")
}

// SyncEnabled runs the daily sync inside `mfide service`. Turn it off to drive
// `mfide sync` from cron instead.
func SyncEnabled() bool {
	return viper.GetBool("sync.enabled")
}

// SyncAt is the daily sync time, "HH:MM" in the server's local time.
func SyncAt() string {
	return viper.GetString("sync.at")
}

// RetentionCurrentFiles is how many imported current-list zips stay on disk.
func RetentionCurrentFiles() int {
	return viper.GetInt("retention.currentFiles")
}

// RetentionRemovedPlayersDays is how long a player FIDE removed is kept before
// being deleted with their history.
func RetentionRemovedPlayersDays() int {
	return viper.GetInt("retention.removedPlayersDays")
}

// RetentionHistoryMonths is how many months of ratings are kept; 0 keeps all.
func RetentionHistoryMonths() int {
	return viper.GetInt("retention.historyMonths")
}

func DbHost() string {
	return viper.GetString("db.host")
}

func DbName() string {
	return viper.GetString("db.name")
}

func DbPassword() string {
	return viper.GetString("db.password")
}

func DbPort() int {
	return viper.GetInt("db.port")
}

func DbUser() string {
	return viper.GetString("db.user")
}

func ServerPrefix() string {
	return viper.GetString("server.prefix")
}

func ServerPort() int {
	return viper.GetInt("server.port")
}

func ServerHost() string {
	return viper.GetString("server.host")
}

func ServerAddr() string {
	return fmt.Sprintf("%s:%d", ServerHost(), ServerPort())
}

func ServiceRoot() string {
	return viper.GetString("service.root")
}

// ServicePath joins elems onto the service root.
func ServicePath(elem ...string) string {
	return filepath.Join(append([]string{ServiceRoot()}, elem...)...)
}
