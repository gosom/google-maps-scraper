package env

import (
	"fmt"
	"os"

	"github.com/gosom/google-maps-scraper/log"
)

func IsEnvSet(name string) bool {
	_, ok := os.LookupEnv(name)

	return ok
}

func LogUnsetEnvs(value ...string) {
	for _, v := range value {
		if !IsEnvSet(v) {
			msg := fmt.Sprintf("environment variable %s is not set. This can be insecure and hould be used only for testing", v)

			log.Warn(msg)
		}
	}
}
