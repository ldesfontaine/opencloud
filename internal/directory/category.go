package directory

import (
	"path"
	"strings"

	"github.com/ldesfontaine/opencloud/internal/registry"
)

// Category dit ce qu'est un service : un serveur web, un SSO, une base de
// données… Elle se déduit de l'image par un petit catalogue ; l'opérateur la
// corrige en la posant. Application ne vient qu'à défaut : l'image est
// inconnue.
type Category string

const (
	CategoryApplication Category = "application"
	CategoryWeb         Category = "web"
	CategorySSO         Category = "sso"
	CategoryDatabase    Category = "database"
	CategorySecurity    Category = "security"
	CategoryMonitoring  Category = "monitoring"
)

func Categories() []Category {
	return []Category{CategoryWeb, CategorySSO, CategoryDatabase, CategorySecurity, CategoryMonitoring, CategoryApplication}
}

// knownImages range les images connues par le dernier segment de leur dépôt
// (« authelia/authelia » est « authelia ») ; une entrée à deux segments
// vise un dépôt dont le nom court ne dirait rien (« goauthentik/server »).
var knownImages = map[string]Category{
	"traefik": CategoryWeb, "nginx": CategoryWeb, "caddy": CategoryWeb, "haproxy": CategoryWeb,
	"httpd": CategoryWeb, "nginx-proxy-manager": CategoryWeb, "envoy": CategoryWeb,

	"authelia": CategorySSO, "authentik": CategorySSO, "keycloak": CategorySSO, "zitadel": CategorySSO,
	"goauthentik/server": CategorySSO,

	"postgres": CategoryDatabase, "mysql": CategoryDatabase, "mariadb": CategoryDatabase, "mongo": CategoryDatabase,
	"redis": CategoryDatabase, "valkey": CategoryDatabase, "memcached": CategoryDatabase, "influxdb": CategoryDatabase,
	"clickhouse-server": CategoryDatabase,

	"crowdsec": CategorySecurity, "fail2ban": CategorySecurity, "wazuh": CategorySecurity,
	"crowdsec-firewall-bouncer": CategorySecurity,

	"prometheus": CategoryMonitoring, "grafana": CategoryMonitoring, "loki": CategoryMonitoring,
	"uptime-kuma": CategoryMonitoring, "netdata": CategoryMonitoring,
}

// CategoryOfImage rend la catégorie d'une image connue ; faux si elle ne
// l'est pas ou si la référence est illisible.
func CategoryOfImage(image string) (Category, bool) {
	reference, err := registry.Parse(image)
	if err != nil {
		return "", false
	}
	repository := strings.TrimPrefix(reference.Repository, "library/")
	if category, known := knownImages[repository]; known {
		return category, true
	}
	category, known := knownImages[path.Base(repository)]
	return category, known
}

// DeducedCategory est ce que l'image de l'unité principale (la première) dit
// du service ; Application si elle est inconnue ou sans unité. Les autres
// unités (le worker, la base d'une application) ne changent pas ce qu'est le
// service.
func (s Service) DeducedCategory() Category {
	if len(s.Units) == 0 {
		return CategoryApplication
	}
	if category, known := CategoryOfImage(s.Units[0].Image); known {
		return category
	}
	return CategoryApplication
}

// ShownCategory est la catégorie que l'écran affiche : celle de l'opérateur
// si elle est posée, sinon celle de l'image.
func (s Service) ShownCategory() Category {
	if s.Category != "" {
		return s.Category
	}
	return s.DeducedCategory()
}
