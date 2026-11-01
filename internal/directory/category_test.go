package directory

import "testing"

func TestCategoryOfImage_ReadsTheRepositoryNotTheTagOrTheRegistry(t *testing.T) {
	cases := map[string]Category{
		"traefik":                        CategoryWeb,
		"traefik:v3.1":                   CategoryWeb,
		"docker.io/library/nginx:1.27":   CategoryWeb,
		"authelia/authelia:4.38":         CategorySSO,
		"ghcr.io/goauthentik/server:1.0": CategorySSO,
		"crowdsecurity/crowdsec:v1.6":    CategorySecurity,
		"postgres@" + digestA:            CategoryDatabase,
		"library/redis":                  CategoryDatabase,
		"grafana/grafana:11.2.0":         CategoryMonitoring,
	}
	for image, want := range cases {
		if got, known := CategoryOfImage(image); !known || got != want {
			t.Errorf("%s : %q (connue %v), attendu %q", image, got, known, want)
		}
	}
	for _, image := range []string{"nextcloud:29", "ghcr.io/org/blog:2.1", "", "pas une image!"} {
		if got, known := CategoryOfImage(image); known {
			t.Errorf("%q ne figure pas au catalogue : %q", image, got)
		}
	}
}

// Application ne vient que si l'image est inconnue ; la correction de
// l'opérateur l'emporte sur l'image, et seule la première unité compte.
func TestService_ShownCategory(t *testing.T) {
	traefik := Service{Units: []Unit{{Image: "traefik"}, {Image: "postgres"}}}
	if got := traefik.ShownCategory(); got != CategoryWeb {
		t.Errorf("traefik : %q", got)
	}
	unknown := Service{Units: []Unit{{Image: "nextcloud"}, {Image: "postgres"}}}
	if got := unknown.ShownCategory(); got != CategoryApplication {
		t.Errorf("l'application avec sa base : %q", got)
	}
	corrected := Service{Category: CategorySecurity, Units: []Unit{{Image: "traefik"}}}
	if got := corrected.ShownCategory(); got != CategorySecurity {
		t.Errorf("corrigée : %q", got)
	}
	if got := (Service{}).ShownCategory(); got != CategoryApplication {
		t.Errorf("sans unité : %q", got)
	}
}

func TestValidate_CategoryIsEmptyOrKnown(t *testing.T) {
	definition := ServiceDefinition{
		MachineID: "m1", Name: "edge", Nature: NatureInfra, Origin: OriginContainer,
		Units: []UnitDefinition{{Name: "t", Image: "traefik", ContainerName: "traefik"}},
	}.Complete()
	if err := definition.Validate(); err != nil {
		t.Fatal(err)
	}
	definition.Category = CategorySSO
	if err := definition.Validate(); err != nil {
		t.Errorf("une catégorie connue : %v", err)
	}
	definition.Category = "serveur"
	if err := definition.Validate(); err != ErrCategoryInvalid {
		t.Errorf("une catégorie inconnue : %v", err)
	}
}
