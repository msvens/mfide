package config

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/spf13/viper"
)

// InitConfig reads config.yaml from $HOME/.mfide, /etc/mfide or the working
// directory. MFIDE_* environment variables override the file (MFIDE_DB_HOST, …).
func InitConfig() error {
	return readConfig("config")
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
	return viper.ReadInConfig()
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
