package role

import (
	"errors"
	"testing"
)

func TestReportValidate_TheClosedList(t *testing.T) {
	tooMany := Report{}
	for range MaxRoles + 1 {
		tooMany.Roles = append(tooMany.Roles, Role{Name: ProxmoxVE})
	}
	cases := map[string]struct {
		report Report
		valid  bool
	}{
		"vide":                {Report{Roles: []Role{}}, true},
		"proxmox":             {Report{Roles: []Role{{Name: ProxmoxVE, Version: "8.2.4"}}}, true},
		"sans version":        {Report{Roles: []Role{{Name: ProxmoxVE}}}, true},
		"version Debian":      {Report{Roles: []Role{{Name: ProxmoxVE, Version: "1:8.2.4~bpo12+1"}}}, true},
		"rôle inconnu":        {Report{Roles: []Role{{Name: "kubernetes"}}}, false},
		"rôle en double":      {Report{Roles: []Role{{Name: ProxmoxVE}, {Name: ProxmoxVE}}}, false},
		"version en HTML":     {Report{Roles: []Role{{Name: ProxmoxVE, Version: "8<b>"}}}, false},
		"version trop longue": {Report{Roles: []Role{{Name: ProxmoxVE, Version: "8." + string(make([]byte, 80))}}}, false},
		"trop de rôles":       {tooMany, false},
	}
	for name, test := range cases {
		err := test.report.Validate()
		if test.valid && err != nil || !test.valid && !errors.Is(err, ErrReportInvalid) {
			t.Errorf("%s : %v", name, err)
		}
	}
}
