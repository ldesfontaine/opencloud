package brand

import (
	"strings"
	"testing"
)

// Chaque valeur est écrite en littéral : renommer le produit doit casser ce
// test, pour que l'auteur voie ce qui bouge avec lui (chemins, protocole).
func TestValues_AreFrozen(t *testing.T) {
	for name, pair := range map[string][2]string{
		"ProductName":             {ProductName, "openCloud"},
		"ID":                      {ID, "opencloud"},
		"ConfigDir":               {ConfigDir, "/etc/opencloud"},
		"ConfigPath":              {ConfigPath, "/etc/opencloud/config.toml"},
		"StateDir":                {StateDir, "/var/lib/opencloud"},
		"AgentStateDir":           {AgentStateDir, "/var/lib/opencloud-agent"},
		"AgentCurrentPath":        {AgentCurrentPath, "/var/lib/opencloud-agent/bin/current"},
		"AgentProduceDir":         {AgentProduceDir, "produce"},
		"VzdumpWrapperPath":       {VzdumpWrapperPath, "/usr/local/sbin/opencloud-vzdump"},
		"AgentAccount":            {AgentAccount, "opencloud-agent"},
		"DatabaseFile":            {DatabaseFile, "opencloud.db"},
		"SecretKeyFile":           {SecretKeyFile, "secret.key"},
		"ServerKeyFile":           {ServerKeyFile, "server.key"},
		"SecretKeyIDDomain":       {SecretKeyIDDomain, "opencloud-key-id\x00"},
		"LocalMachineName":        {LocalMachineName, "opencloud"},
		"MatrixTransactionPrefix": {MatrixTransactionPrefix, "opencloud-"},
		"OutgoingDir":             {OutgoingDir, "outgoing"},
		"BackupKeyFilePrefix":     {BackupKeyFilePrefix, "opencloud-key-"},
		"LabelPrefix":             {LabelPrefix, "opencloud."},
		"WorkspaceMarker":         {WorkspaceMarker, ".opencloud"},
		"ServerUnit":              {ServerUnit, "opencloud.service"},
		"AgentUnit":               {AgentUnit, "opencloud-agent.service"},
		"Command":                 {Command, "opencloud"},
		"UserAgent":               {UserAgent, "openCloud"},
		"RegistryUserAgent":       {RegistryUserAgent, "openCloud-agent"},
		"LifecycleUserAgent":      {LifecycleUserAgent, "opencloud"},
		"HeaderPrefix":            {HeaderPrefix, "OpenCloud-"},
		"WebhookEventHeader":      {WebhookEventHeader, "OpenCloud-Event"},
		"WebhookSignatureHeader":  {WebhookSignatureHeader, "OpenCloud-Signature"},
		"ProxySecretHeader":       {ProxySecretHeader, "X-OpenCloud-Proxy-Secret"},
		"OrderSignatureDomain":    {OrderSignatureDomain, "opencloud-order/v1\x00"},
		"AgentProofDomain":        {AgentProofDomain, "opencloud-agent-proof/v2\x00"},
		"TokenPrefix":             {TokenPrefix, "oc"},
		"EnrollmentTokenScheme":   {EnrollmentTokenScheme, "oc_"},
		"APITokenScheme":          {APITokenScheme, "ock_"},
		"HostFingerprintPrefix":   {HostFingerprintPrefix, "opencloud-host:"},
	} {
		if pair[0] != pair[1] {
			t.Errorf("%s = %q, want %q", name, pair[0], pair[1])
		}
		// Ces valeurs entrent dans des formats et des gabarits : un « % » ou
		// une accolade y serait lu comme un verbe ou un marqueur.
		if strings.ContainsAny(pair[0], "%{}") {
			t.Errorf("%s = %q contains %%, { or }", name, pair[0])
		}
	}
}
