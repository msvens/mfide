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
	return viper.ReadInConfig()
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
