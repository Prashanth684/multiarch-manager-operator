package system_config

import (
	"fmt"
	"github.com/BurntSushi/toml"
	"k8s.io/apimachinery/pkg/util/json"
	"os"
	"path/filepath"
	"strings"
)

const (
	RegistriesConfPath = "/tmp/containers/registries.conf"
	PolicyConfPath     = "/tmp/containers/policy.json"
	DockerCertsDir     = "/tmp/docker/certs.d"
	RegistryCertsDir   = "/tmp/containers/registries.d"
)

type registryCertTuple struct {
	registry string
	cert     string
}

func (t registryCertTuple) writeToFile() error {
	// create folder if it doesn't exist
	absoluteFolderPath := fmt.Sprintf("%s/%s", DockerCertsDir, t.getFolderName())
	if _, err := os.Stat(absoluteFolderPath); os.IsNotExist(err) {
		err = os.MkdirAll(absoluteFolderPath, 0755)
		if err != nil {
			return err
		}
	}
	// write cert to file
	absoluteFilePath := fmt.Sprintf("%s/%s/ca.crt", DockerCertsDir, t.getFolderName())
	f, err := os.Create(absoluteFilePath)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(t.cert)
	if err != nil {
		return err
	}
	return nil
}

func (t registryCertTuple) getFolderName() string {
	// the registry name could report the port number after two dots, e.g. registry.example.com..5000.
	// we need to replace the two dots with a colon to get the correct folder name.
	return strings.Replace(t.registry, "..", ":", 1)
}

type registriesConf struct {
	UnqualifiedSearchRegistries []string                 `toml:"unqualified-search-registries"`
	ShortNameMode               string                   `toml:"short-name-mode"`
	Registries                  []*registryConf          `toml:"registry"`
	registriesMap               map[string]*registryConf `toml:"-"`
}

func (rsc *registriesConf) getRegistryConfOrCreate(registry string) *registryConf {
	rc, _ := rsc.registriesMap[registry]
	if rc == nil {
		rc = &registryConf{
			Location: registry,
		}
		rsc.registriesMap[registry] = rc
		rsc.Registries = append(rsc.Registries, rc)
	}
	return rc
}

func (rsc *registriesConf) writeToFile() error {
	return writeTomlFile(RegistriesConfPath, rsc)
}

func (rsc *registriesConf) getRegistryConf(registry string) (*registryConf, bool) {
	rc, ok := rsc.registriesMap[registry]
	return rc, ok
}

func (rsc *registriesConf) cleanupRegistryConfIfEmpty(registry string) {
	if rc, ok := rsc.getRegistryConf(registry); ok {
		if rc.Insecure == nil && rc.Allowed == nil && rc.Blocked == nil && len(rc.Mirrors) == 0 {
			delete(rsc.registriesMap, registry)
			for i, r := range rsc.Registries {
				if r == rc {
					rsc.Registries = append(rsc.Registries[:i], rsc.Registries[i+1:]...)
					break
				}
			}
		}
	}
}

func (rsc *registriesConf) cleanupAllRegistryConfIfEmpty() {
	for _, registry := range rsc.Registries {
		rsc.cleanupRegistryConfIfEmpty(registry.Location)
	}
}

type registryConf struct {
	Location string   `toml:"location"`
	Prefix   string   `toml:"prefix"`
	Mirrors  []Mirror `toml:"mirror"`
	// Setting the blocked, allowed and insecure fields to nil will cause them to be omitted from the output
	Blocked  *bool `toml:"blocked"`
	Allowed  *bool `toml:"allowed"`
	Insecure *bool `toml:"insecure"`
}

type Mirror struct {
	Location string `toml:"location"`
	// insecure *bool  `toml:"insecure"`
}

func mirrorFor(location string) Mirror {
	return Mirror{
		Location: location,
		// insecure: insecure,
	}
}

func mirrorsFor(locations []string) []Mirror {
	var mirrors []Mirror
	for _, location := range locations {
		mirrors = append(mirrors, mirrorFor(location))
	}
	return mirrors
}

// defaultRegistriesConf returns a default registriesConf object
func defaultRegistriesConf() registriesConf {
	return registriesConf{
		UnqualifiedSearchRegistries: []string{"registry.access.redhat.com", "docker.io"},
		ShortNameMode:               "",
		Registries:                  []*registryConf{},
		registriesMap:               map[string]*registryConf{},
	}
}

const (
	dockerDaemonTransport = "docker-daemon"
	dockerTransport       = "docker"
	atomicTransport       = "atomic"
)

// {"default":[{"type":"insecureAcceptAnything"}],"transports":{"atomic":{"docker.io":[{"type":"reject"}]},"docker":{"docker.io":[{"type":"reject"}]},"docker-daemon":{"":[{"type":"insecureAcceptAnything"}]}}}
type policyConf struct {
	Default    []policyEntry                       `json:"default"`
	Transports map[string]map[string][]policyEntry `json:"transports"`
}

func (pc policyConf) resetTransports() {
	pc.Transports = defaultTransports()
}

func (pc policyConf) setRejectForRegistry(registry string) {
	pc.setRejectForRegistryOnTransport(registry, dockerTransport)
	pc.setRejectForRegistryOnTransport(registry, atomicTransport)
}

func (pc policyConf) setRejectForRegistryOnTransport(registry, transport string) {
	pc.Transports[transport][registry] = []policyEntry{
		rejectPolicyEntry(),
	}
}

func (pc policyConf) writeToFile() error {
	return writeJSONFile(PolicyConfPath, pc)
}

// defaultPolicyConf returns a default policyConf object
func defaultPolicyConf() policyConf {
	return policyConf{
		Default: []policyEntry{
			insecureAcceptAnythingPolicyEntry(),
		},
		Transports: defaultTransports(),
	}
}

func defaultTransports() map[string]map[string][]policyEntry {
	return map[string]map[string][]policyEntry{
		dockerDaemonTransport: {
			"": []policyEntry{
				insecureAcceptAnythingPolicyEntry(),
			},
		},
		atomicTransport: {},
		dockerTransport: {},
	}
}

func insecureAcceptAnythingPolicyEntry() policyEntry {
	return policyEntry{
		Type: "insecureAcceptAnything",
	}
}

func rejectPolicyEntry() policyEntry {
	return policyEntry{
		Type: "reject",
	}
}

type policyEntry struct {
	Type string `json:"type"`
}

func writeTomlFile(path string, data interface{}) error {
	createBaseDir(path)
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return toml.NewEncoder(f).Encode(data)
}

func createBaseDir(path string) {
	// create base dir if it doesn't exist
	baseDir := filepath.Dir(path)
	if _, err := os.Stat(baseDir); os.IsNotExist(err) {
		os.MkdirAll(baseDir, os.ModePerm)
	}
}

func writeJSONFile(path string, data interface{}) error {
	createBaseDir(path)
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewEncoder(f).Encode(data)
}

/* example policy.json
{
  "default": [
    {
      "type": "insecureAcceptAnything"
    }
  ],
  "transports": {
    "atomic": {
      "docker.io": [
        {
          "type": "reject"
        }
      ]
    },
    "docker": {
      "docker.io": [
        {
          "type": "reject"
        }
      ]
    },
    "docker-daemon": {
      "": [
        {
          "type": "insecureAcceptAnything"
        }
      ]
    }
  }
}

*/
