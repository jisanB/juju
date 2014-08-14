// Copyright 2013 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package maas

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io/ioutil"
	"net/url"
	"strings"
	"text/template"

	"github.com/juju/errors"
	jc "github.com/juju/testing/checkers"
	"github.com/juju/utils"
	"github.com/juju/utils/set"
	goyaml "gopkg.in/yaml.v1"
	gc "launchpad.net/gocheck"
	"launchpad.net/gomaasapi"

	"github.com/juju/juju/constraints"
	"github.com/juju/juju/environs"
	"github.com/juju/juju/environs/bootstrap"
	"github.com/juju/juju/environs/config"
	"github.com/juju/juju/environs/imagemetadata"
	"github.com/juju/juju/environs/simplestreams"
	"github.com/juju/juju/environs/storage"
	envtesting "github.com/juju/juju/environs/testing"
	envtools "github.com/juju/juju/environs/tools"
	"github.com/juju/juju/instance"
	"github.com/juju/juju/juju/testing"
	"github.com/juju/juju/network"
	"github.com/juju/juju/provider/common"
	coretesting "github.com/juju/juju/testing"
	"github.com/juju/juju/tools"
	"github.com/juju/juju/version"
)

type environSuite struct {
	providerSuite
}

const (
	allocatedNode = `{"system_id": "test-allocated"}`
)

var _ = gc.Suite(&environSuite{})

// getTestConfig creates a customized sample MAAS provider configuration.
func getTestConfig(name, server, oauth, secret string) *config.Config {
	ecfg, err := newConfig(map[string]interface{}{
		"name":            name,
		"maas-server":     server,
		"maas-oauth":      oauth,
		"admin-secret":    secret,
		"authorized-keys": "I-am-not-a-real-key",
	})
	if err != nil {
		panic(err)
	}
	return ecfg.Config
}

func (suite *environSuite) setupFakeTools(c *gc.C) {
	stor := NewStorage(suite.makeEnviron())
	envtesting.UploadFakeTools(c, stor)
}

func (suite *environSuite) setupFakeImageMetadata(c *gc.C) {
	stor := NewStorage(suite.makeEnviron())
	UseTestImageMetadata(c, stor)
}

func (suite *environSuite) addNode(jsonText string) instance.Id {
	node := suite.testMAASObject.TestServer.NewNode(jsonText)
	resourceURI, _ := node.GetField("resource_uri")
	return instance.Id(resourceURI)
}

func (suite *environSuite) TestInstancesReturnsInstances(c *gc.C) {
	id := suite.addNode(allocatedNode)
	instances, err := suite.makeEnviron().Instances([]instance.Id{id})

	c.Check(err, gc.IsNil)
	c.Assert(instances, gc.HasLen, 1)
	c.Assert(instances[0].Id(), gc.Equals, id)
}

func (suite *environSuite) TestInstancesReturnsErrNoInstancesIfEmptyParameter(c *gc.C) {
	suite.addNode(allocatedNode)
	instances, err := suite.makeEnviron().Instances([]instance.Id{})

	c.Check(err, gc.Equals, environs.ErrNoInstances)
	c.Check(instances, gc.IsNil)
}

func (suite *environSuite) TestInstancesReturnsErrNoInstancesIfNilParameter(c *gc.C) {
	suite.addNode(allocatedNode)
	instances, err := suite.makeEnviron().Instances(nil)

	c.Check(err, gc.Equals, environs.ErrNoInstances)
	c.Check(instances, gc.IsNil)
}

func (suite *environSuite) TestInstancesReturnsErrNoInstancesIfNoneFound(c *gc.C) {
	instances, err := suite.makeEnviron().Instances([]instance.Id{"unknown"})
	c.Check(err, gc.Equals, environs.ErrNoInstances)
	c.Check(instances, gc.IsNil)
}

func (suite *environSuite) TestAllInstances(c *gc.C) {
	id := suite.addNode(allocatedNode)
	instances, err := suite.makeEnviron().AllInstances()

	c.Check(err, gc.IsNil)
	c.Assert(instances, gc.HasLen, 1)
	c.Assert(instances[0].Id(), gc.Equals, id)
}

func (suite *environSuite) TestAllInstancesReturnsEmptySliceIfNoInstance(c *gc.C) {
	instances, err := suite.makeEnviron().AllInstances()

	c.Check(err, gc.IsNil)
	c.Check(instances, gc.HasLen, 0)
}

func (suite *environSuite) TestInstancesReturnsErrorIfPartialInstances(c *gc.C) {
	known := suite.addNode(allocatedNode)
	suite.addNode(`{"system_id": "test2"}`)
	unknown := instance.Id("unknown systemID")
	instances, err := suite.makeEnviron().Instances([]instance.Id{known, unknown})

	c.Check(err, gc.Equals, environs.ErrPartialInstances)
	c.Assert(instances, gc.HasLen, 2)
	c.Check(instances[0].Id(), gc.Equals, known)
	c.Check(instances[1], gc.IsNil)
}

func (suite *environSuite) TestStorageReturnsStorage(c *gc.C) {
	env := suite.makeEnviron()
	stor := env.Storage()
	c.Check(stor, gc.NotNil)
	// The Storage object is really a maasStorage.
	specificStorage := stor.(*maasStorage)
	// Its environment pointer refers back to its environment.
	c.Check(specificStorage.environUnlocked, gc.Equals, env)
}

func decodeUserData(userData string) ([]byte, error) {
	data, err := base64.StdEncoding.DecodeString(userData)
	if err != nil {
		return []byte(""), err
	}
	return utils.Gunzip(data)
}

const lshwXMLTemplate = `
<?xml version="1.0" standalone="yes" ?>
<!-- generated by lshw-B.02.16 -->
<list>
<node id="node1" claimed="true" class="system" handle="DMI:0001">
 <description>Computer</description>
 <product>VirtualBox ()</product>
 <width units="bits">64</width>
  <node id="core" claimed="true" class="bus" handle="DMI:0008">
   <description>Motherboard</description>
    <node id="pci" claimed="true" class="bridge" handle="PCIBUS:0000:00">
     <description>Host bridge</description>{{range $m, $n := .}}
      <node id="network:0" claimed="true" class="network" handle="PCI:0000:00:03.0">
       <description>Ethernet interface</description>
       <product>82540EM Gigabit Ethernet Controller</product>
       <logicalname>{{$n}}</logicalname>
       <serial>{{$m}}</serial>
      </node>{{end}}
    </node>
  </node>
</node>
</list>
</list>
`

func (suite *environSuite) generateHWTemplate(netMacs map[string]string) (string, error) {
	tmpl, err := template.New("test").Parse(lshwXMLTemplate)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	err = tmpl.Execute(&buf, netMacs)
	if err != nil {
		return "", err
	}
	return string(buf.Bytes()), nil
}

func (suite *environSuite) TestStartInstanceStartsInstance(c *gc.C) {
	suite.setupFakeTools(c)
	env := suite.makeEnviron()
	// Create node 0: it will be used as the bootstrap node.
	suite.testMAASObject.TestServer.NewNode(`{"system_id": "node0", "hostname": "host0"}`)
	lshwXML, err := suite.generateHWTemplate(map[string]string{"aa:bb:cc:dd:ee:f0": "eth0"})
	c.Assert(err, gc.IsNil)
	suite.testMAASObject.TestServer.AddNodeDetails("node0", lshwXML)
	err = bootstrap.Bootstrap(coretesting.Context(c), env, environs.BootstrapParams{})
	c.Assert(err, gc.IsNil)
	// The bootstrap node has been acquired and started.
	operations := suite.testMAASObject.TestServer.NodeOperations()
	actions, found := operations["node0"]
	c.Check(found, gc.Equals, true)
	c.Check(actions, gc.DeepEquals, []string{"acquire", "start"})

	// Test the instance id is correctly recorded for the bootstrap node.
	// Check that StateServerInstances returns the id of the bootstrap machine.
	instanceIds, err := env.StateServerInstances()
	c.Assert(err, gc.IsNil)
	c.Assert(instanceIds, gc.HasLen, 1)
	insts, err := env.AllInstances()
	c.Assert(err, gc.IsNil)
	c.Assert(insts, gc.HasLen, 1)
	c.Check(insts[0].Id(), gc.Equals, instanceIds[0])

	// Create node 1: it will be used as instance number 1.
	suite.testMAASObject.TestServer.NewNode(`{"system_id": "node1", "hostname": "host1"}`)
	lshwXML, err = suite.generateHWTemplate(map[string]string{"aa:bb:cc:dd:ee:f1": "eth0"})
	c.Assert(err, gc.IsNil)
	suite.testMAASObject.TestServer.AddNodeDetails("node1", lshwXML)
	// TODO(wallyworld) - test instance metadata
	instance, _ := testing.AssertStartInstance(c, env, "1")
	c.Assert(err, gc.IsNil)
	c.Check(instance, gc.NotNil)

	// The instance number 1 has been acquired and started.
	actions, found = operations["node1"]
	c.Assert(found, gc.Equals, true)
	c.Check(actions, gc.DeepEquals, []string{"acquire", "start"})

	// The value of the "user data" parameter used when starting the node
	// contains the run cmd used to write the machine information onto
	// the node's filesystem.
	requestValues := suite.testMAASObject.TestServer.NodeOperationRequestValues()
	nodeRequestValues, found := requestValues["node1"]
	c.Assert(found, gc.Equals, true)
	c.Assert(len(nodeRequestValues), gc.Equals, 2)
	userData := nodeRequestValues[1].Get("user_data")
	decodedUserData, err := decodeUserData(userData)
	c.Assert(err, gc.IsNil)
	info := machineInfo{"host1"}
	cloudinitRunCmd, err := info.cloudinitRunCmd()
	c.Assert(err, gc.IsNil)
	data, err := goyaml.Marshal(cloudinitRunCmd)
	c.Assert(err, gc.IsNil)
	c.Check(string(decodedUserData), gc.Matches, "(.|\n)*"+string(data)+"(\n|.)*")

	// Trash the tools and try to start another instance.
	envtesting.RemoveTools(c, env.Storage())
	instance, _, _, err = testing.StartInstance(env, "2")
	c.Check(instance, gc.IsNil)
	c.Check(err, jc.Satisfies, errors.IsNotFound)
}

func uint64p(val uint64) *uint64 {
	return &val
}

func stringp(val string) *string {
	return &val
}

func (suite *environSuite) TestAcquireNode(c *gc.C) {
	stor := NewStorage(suite.makeEnviron())
	fakeTools := envtesting.MustUploadFakeToolsVersions(stor, version.Current)[0]
	env := suite.makeEnviron()
	suite.testMAASObject.TestServer.NewNode(`{"system_id": "node0", "hostname": "host0"}`)

	_, _, err := env.acquireNode("", constraints.Value{}, nil, nil, tools.List{fakeTools})

	c.Check(err, gc.IsNil)
	operations := suite.testMAASObject.TestServer.NodeOperations()
	actions, found := operations["node0"]
	c.Assert(found, gc.Equals, true)
	c.Check(actions, gc.DeepEquals, []string{"acquire"})

	// no "name" parameter should have been passed through
	values := suite.testMAASObject.TestServer.NodeOperationRequestValues()["node0"][0]
	_, found = values["name"]
	c.Assert(found, jc.IsFalse)
}

func (suite *environSuite) TestAcquireNodeByName(c *gc.C) {
	stor := NewStorage(suite.makeEnviron())
	fakeTools := envtesting.MustUploadFakeToolsVersions(stor, version.Current)[0]
	env := suite.makeEnviron()
	suite.testMAASObject.TestServer.NewNode(`{"system_id": "node0", "hostname": "host0"}`)

	_, _, err := env.acquireNode("host0", constraints.Value{}, nil, nil, tools.List{fakeTools})

	c.Check(err, gc.IsNil)
	operations := suite.testMAASObject.TestServer.NodeOperations()
	actions, found := operations["node0"]
	c.Assert(found, gc.Equals, true)
	c.Check(actions, gc.DeepEquals, []string{"acquire"})

	// no "name" parameter should have been passed through
	values := suite.testMAASObject.TestServer.NodeOperationRequestValues()["node0"][0]
	nodeName := values.Get("name")
	c.Assert(nodeName, gc.Equals, "host0")
}

func (suite *environSuite) TestAcquireNodeTakesConstraintsIntoAccount(c *gc.C) {
	stor := NewStorage(suite.makeEnviron())
	fakeTools := envtesting.MustUploadFakeToolsVersions(stor, version.Current)[0]
	env := suite.makeEnviron()
	suite.testMAASObject.TestServer.NewNode(`{"system_id": "node0", "hostname": "host0"}`)
	constraints := constraints.Value{Arch: stringp("arm"), Mem: uint64p(1024)}

	_, _, err := env.acquireNode("", constraints, nil, nil, tools.List{fakeTools})

	c.Check(err, gc.IsNil)
	requestValues := suite.testMAASObject.TestServer.NodeOperationRequestValues()
	nodeRequestValues, found := requestValues["node0"]
	c.Assert(found, gc.Equals, true)
	c.Assert(nodeRequestValues[0].Get("arch"), gc.Equals, "arm")
	c.Assert(nodeRequestValues[0].Get("mem"), gc.Equals, "1024")
}

func (suite *environSuite) TestAcquireNodePassedAgentName(c *gc.C) {
	stor := NewStorage(suite.makeEnviron())
	fakeTools := envtesting.MustUploadFakeToolsVersions(stor, version.Current)[0]
	env := suite.makeEnviron()
	suite.testMAASObject.TestServer.NewNode(`{"system_id": "node0", "hostname": "host0"}`)

	_, _, err := env.acquireNode("", constraints.Value{}, nil, nil, tools.List{fakeTools})

	c.Check(err, gc.IsNil)
	requestValues := suite.testMAASObject.TestServer.NodeOperationRequestValues()
	nodeRequestValues, found := requestValues["node0"]
	c.Assert(found, gc.Equals, true)
	c.Assert(nodeRequestValues[0].Get("agent_name"), gc.Equals, exampleAgentName)
}

var testValues = []struct {
	constraints    constraints.Value
	expectedResult url.Values
}{
	{constraints.Value{Arch: stringp("arm")}, url.Values{"arch": {"arm"}}},
	{constraints.Value{CpuCores: uint64p(4)}, url.Values{"cpu_count": {"4"}}},
	{constraints.Value{Mem: uint64p(1024)}, url.Values{"mem": {"1024"}}},

	// CpuPower is ignored.
	{constraints.Value{CpuPower: uint64p(1024)}, url.Values{}},

	// RootDisk is ignored.
	{constraints.Value{RootDisk: uint64p(8192)}, url.Values{}},
	{constraints.Value{Tags: &[]string{"foo", "bar"}}, url.Values{"tags": {"foo,bar"}}},
	{constraints.Value{Arch: stringp("arm"), CpuCores: uint64p(4), Mem: uint64p(1024), CpuPower: uint64p(1024), RootDisk: uint64p(8192), Tags: &[]string{"foo", "bar"}}, url.Values{"arch": {"arm"}, "cpu_count": {"4"}, "mem": {"1024"}, "tags": {"foo,bar"}}},
}

func (*environSuite) TestConvertConstraints(c *gc.C) {
	for _, test := range testValues {
		c.Check(convertConstraints(test.constraints), gc.DeepEquals, test.expectedResult)
	}
}

var testNetworkValues = []struct {
	includeNetworks []string
	excludeNetworks []string
	expectedResult  url.Values
}{
	{
		nil,
		nil,
		url.Values{},
	},
	{
		[]string{"included_net_1"},
		nil,
		url.Values{"networks": {"included_net_1"}},
	},
	{
		nil,
		[]string{"excluded_net_1"},
		url.Values{"not_networks": {"excluded_net_1"}},
	},
	{
		[]string{"included_net_1", "included_net_2"},
		[]string{"excluded_net_1", "excluded_net_2"},
		url.Values{
			"networks":     {"included_net_1", "included_net_2"},
			"not_networks": {"excluded_net_1", "excluded_net_2"},
		},
	},
}

func (*environSuite) TestConvertNetworks(c *gc.C) {
	for _, test := range testNetworkValues {
		var vals = url.Values{}
		addNetworks(vals, test.includeNetworks, test.excludeNetworks)
		c.Check(vals, gc.DeepEquals, test.expectedResult)
	}
}

func (suite *environSuite) getInstance(systemId string) *maasInstance {
	input := fmt.Sprintf(`{"system_id": %q}`, systemId)
	node := suite.testMAASObject.TestServer.NewNode(input)
	return &maasInstance{maasObject: &node, environ: suite.makeEnviron()}
}

func (suite *environSuite) getNetwork(name string, id int, vlanTag int) *gomaasapi.MAASObject {
	var vlan string
	if vlanTag == 0 {
		vlan = "null"
	} else {
		vlan = fmt.Sprintf("%d", vlanTag)
	}
	var input string
	input = fmt.Sprintf(`{"name": %q, "ip":"192.168.%d.1", "netmask": "255.255.255.0",`+
		`"vlan_tag": %s, "description": "%s_%d_%d" }`, name, id, vlan, name, id, vlanTag)
	network := suite.testMAASObject.TestServer.NewNetwork(input)
	return &network
}

func (suite *environSuite) TestStopInstancesReturnsIfParameterEmpty(c *gc.C) {
	suite.getInstance("test1")

	err := suite.makeEnviron().StopInstances()
	c.Check(err, gc.IsNil)
	operations := suite.testMAASObject.TestServer.NodeOperations()
	c.Check(operations, gc.DeepEquals, map[string][]string{})
}

func (suite *environSuite) TestStopInstancesStopsAndReleasesInstances(c *gc.C) {
	suite.getInstance("test1")
	suite.getInstance("test2")
	suite.getInstance("test3")
	// mark test1 and test2 as being allocated, but not test3.
	// The release operation will ignore test3.
	suite.testMAASObject.TestServer.OwnedNodes()["test1"] = true
	suite.testMAASObject.TestServer.OwnedNodes()["test2"] = true

	err := suite.makeEnviron().StopInstances("test1", "test2", "test3")
	c.Check(err, gc.IsNil)
	operations := suite.testMAASObject.TestServer.NodesOperations()
	c.Check(operations, gc.DeepEquals, []string{"release"})
	c.Assert(suite.testMAASObject.TestServer.OwnedNodes()["test1"], jc.IsFalse)
	c.Assert(suite.testMAASObject.TestServer.OwnedNodes()["test2"], jc.IsFalse)
}

func (suite *environSuite) TestStateServerInstances(c *gc.C) {
	env := suite.makeEnviron()
	_, err := env.StateServerInstances()
	c.Assert(err, gc.Equals, environs.ErrNotBootstrapped)

	tests := [][]instance.Id{{}, {"inst-0"}, {"inst-0", "inst-1"}}
	for _, expected := range tests {
		err := common.SaveState(env.Storage(), &common.BootstrapState{
			StateInstances: expected,
		})
		c.Assert(err, gc.IsNil)
		stateServerInstances, err := env.StateServerInstances()
		c.Assert(err, gc.IsNil)
		c.Assert(stateServerInstances, jc.SameContents, expected)
	}
}

func (suite *environSuite) TestStateServerInstancesFailsIfNoStateInstances(c *gc.C) {
	env := suite.makeEnviron()
	_, err := env.StateServerInstances()
	c.Check(err, gc.Equals, environs.ErrNotBootstrapped)
}

func (suite *environSuite) TestDestroy(c *gc.C) {
	env := suite.makeEnviron()
	suite.getInstance("test1")
	suite.testMAASObject.TestServer.OwnedNodes()["test1"] = true // simulate acquire
	data := makeRandomBytes(10)
	suite.testMAASObject.TestServer.NewFile("filename", data)
	stor := env.Storage()

	err := env.Destroy()
	c.Check(err, gc.IsNil)

	// Instances have been stopped.
	operations := suite.testMAASObject.TestServer.NodesOperations()
	c.Check(operations, gc.DeepEquals, []string{"release"})
	c.Check(suite.testMAASObject.TestServer.OwnedNodes()["test1"], jc.IsFalse)
	// Files have been cleaned up.
	listing, err := storage.List(stor, "")
	c.Assert(err, gc.IsNil)
	c.Check(listing, gc.DeepEquals, []string{})
}

func (suite *environSuite) TestBootstrapSucceeds(c *gc.C) {
	suite.setupFakeTools(c)
	env := suite.makeEnviron()
	suite.testMAASObject.TestServer.NewNode(`{"system_id": "thenode", "hostname": "host"}`)
	lshwXML, err := suite.generateHWTemplate(map[string]string{"aa:bb:cc:dd:ee:f0": "eth0"})
	c.Assert(err, gc.IsNil)
	suite.testMAASObject.TestServer.AddNodeDetails("thenode", lshwXML)
	err = bootstrap.Bootstrap(coretesting.Context(c), env, environs.BootstrapParams{})
	c.Assert(err, gc.IsNil)
}

func (suite *environSuite) TestBootstrapFailsIfNoTools(c *gc.C) {
	suite.setupFakeTools(c)
	env := suite.makeEnviron()
	// Can't RemoveAllTools, no public storage.
	envtesting.RemoveTools(c, env.Storage())
	// Disable auto-uploading by setting the agent version.
	cfg, err := env.Config().Apply(map[string]interface{}{
		"agent-version": version.Current.Number.String(),
	})
	c.Assert(err, gc.IsNil)
	err = env.SetConfig(cfg)
	c.Assert(err, gc.IsNil)
	err = bootstrap.Bootstrap(coretesting.Context(c), env, environs.BootstrapParams{})
	stripped := strings.Replace(err.Error(), "\n", "", -1)
	c.Check(stripped,
		gc.Matches,
		"cannot upload bootstrap tools: Juju cannot bootstrap because no tools are available for your environment.*")
}

func (suite *environSuite) TestBootstrapFailsIfNoNodes(c *gc.C) {
	suite.setupFakeTools(c)
	env := suite.makeEnviron()
	err := bootstrap.Bootstrap(coretesting.Context(c), env, environs.BootstrapParams{})
	// Since there are no nodes, the attempt to allocate one returns a
	// 409: Conflict.
	c.Check(err, gc.ErrorMatches, ".*409.*")
}

func assertSourceContents(c *gc.C, source simplestreams.DataSource, filename string, content []byte) {
	rc, _, err := source.Fetch(filename)
	c.Assert(err, gc.IsNil)
	defer rc.Close()
	retrieved, err := ioutil.ReadAll(rc)
	c.Assert(err, gc.IsNil)
	c.Assert(retrieved, gc.DeepEquals, content)
}

func (suite *environSuite) assertGetImageMetadataSources(c *gc.C, stream, officialSourcePath string) {
	// Make an env configured with the stream.
	testAttrs := maasEnvAttrs
	testAttrs = testAttrs.Merge(coretesting.Attrs{
		"maas-server": suite.testMAASObject.TestServer.URL,
	})
	if stream != "" {
		testAttrs = testAttrs.Merge(coretesting.Attrs{
			"image-stream": stream,
		})
	}
	attrs := coretesting.FakeConfig().Merge(testAttrs)
	cfg, err := config.New(config.NoDefaults, attrs)
	c.Assert(err, gc.IsNil)
	env, err := NewEnviron(cfg)
	c.Assert(err, gc.IsNil)

	// Add a dummy file to storage so we can use that to check the
	// obtained source later.
	data := makeRandomBytes(10)
	stor := NewStorage(env)
	err = stor.Put("images/filename", bytes.NewBuffer([]byte(data)), int64(len(data)))
	c.Assert(err, gc.IsNil)
	sources, err := imagemetadata.GetMetadataSources(env)
	c.Assert(err, gc.IsNil)
	c.Assert(len(sources), gc.Equals, 2)
	assertSourceContents(c, sources[0], "filename", data)
	url, err := sources[1].URL("")
	c.Assert(err, gc.IsNil)
	c.Assert(url, gc.Equals, fmt.Sprintf("http://cloud-images.ubuntu.com/%s/", officialSourcePath))
}

func (suite *environSuite) TestGetImageMetadataSources(c *gc.C) {
	suite.assertGetImageMetadataSources(c, "", "releases")
	suite.assertGetImageMetadataSources(c, "released", "releases")
	suite.assertGetImageMetadataSources(c, "daily", "daily")
}

func (suite *environSuite) TestGetToolsMetadataSources(c *gc.C) {
	env := suite.makeEnviron()
	// Add a dummy file to storage so we can use that to check the
	// obtained source later.
	data := makeRandomBytes(10)
	stor := NewStorage(env)
	err := stor.Put("tools/filename", bytes.NewBuffer([]byte(data)), int64(len(data)))
	c.Assert(err, gc.IsNil)
	sources, err := envtools.GetMetadataSources(env)
	c.Assert(err, gc.IsNil)
	c.Assert(len(sources), gc.Equals, 1)
	assertSourceContents(c, sources[0], "filename", data)
}

func (suite *environSuite) TestSupportedArchitectures(c *gc.C) {
	suite.setupFakeImageMetadata(c)
	env := suite.makeEnviron()
	a, err := env.SupportedArchitectures()
	c.Assert(err, gc.IsNil)
	c.Assert(a, jc.SameContents, []string{"amd64"})
}

func (suite *environSuite) TestConstraintsValidator(c *gc.C) {
	suite.setupFakeImageMetadata(c)
	env := suite.makeEnviron()
	validator, err := env.ConstraintsValidator()
	c.Assert(err, gc.IsNil)
	cons := constraints.MustParse("arch=amd64 cpu-power=10 instance-type=foo")
	unsupported, err := validator.Validate(cons)
	c.Assert(err, gc.IsNil)
	c.Assert(unsupported, jc.SameContents, []string{"cpu-power", "instance-type"})
}

func (suite *environSuite) TestConstraintsValidatorVocab(c *gc.C) {
	suite.setupFakeImageMetadata(c)
	env := suite.makeEnviron()
	validator, err := env.ConstraintsValidator()
	c.Assert(err, gc.IsNil)
	cons := constraints.MustParse("arch=ppc64el")
	_, err = validator.Validate(cons)
	c.Assert(err, gc.ErrorMatches, "invalid constraint value: arch=ppc64el\nvalid values are:.*")
}

func (suite *environSuite) TestGetNetworkMACs(c *gc.C) {
	suite.setupFakeTools(c)
	env := suite.makeEnviron()

	suite.testMAASObject.TestServer.NewNode(`{"system_id": "node_1"}`)
	suite.testMAASObject.TestServer.NewNode(`{"system_id": "node_2"}`)
	suite.testMAASObject.TestServer.NewNetwork(`{"name": "net_1"}`)
	suite.testMAASObject.TestServer.NewNetwork(`{"name": "net_2"}`)
	suite.testMAASObject.TestServer.ConnectNodeToNetworkWithMACAddress("node_2", "net_2", "aa:bb:cc:dd:ee:22")
	suite.testMAASObject.TestServer.ConnectNodeToNetworkWithMACAddress("node_1", "net_1", "aa:bb:cc:dd:ee:11")
	suite.testMAASObject.TestServer.ConnectNodeToNetworkWithMACAddress("node_2", "net_1", "aa:bb:cc:dd:ee:21")
	suite.testMAASObject.TestServer.ConnectNodeToNetworkWithMACAddress("node_1", "net_2", "aa:bb:cc:dd:ee:12")

	networks, err := env.getNetworkMACs("net_1")
	c.Assert(err, gc.IsNil)
	c.Check(networks, jc.SameContents, []string{"aa:bb:cc:dd:ee:11", "aa:bb:cc:dd:ee:21"})

	networks, err = env.getNetworkMACs("net_2")
	c.Assert(err, gc.IsNil)
	c.Check(networks, jc.SameContents, []string{"aa:bb:cc:dd:ee:12", "aa:bb:cc:dd:ee:22"})

	networks, err = env.getNetworkMACs("net_3")
	c.Check(networks, gc.HasLen, 0)
	c.Assert(err, gc.IsNil)
}

func (suite *environSuite) TestGetInstanceNetworks(c *gc.C) {
	suite.getNetwork("test_network", 123, 321)
	test_instance := suite.getInstance("instance_for_network")
	suite.testMAASObject.TestServer.ConnectNodeToNetwork("instance_for_network", "test_network")
	networks, err := suite.makeEnviron().getInstanceNetworks(test_instance)
	c.Assert(err, gc.IsNil)
	c.Check(networks, gc.DeepEquals, []networkDetails{
		{Name: "test_network", IP: "192.168.123.1", Mask: "255.255.255.0", VLANTag: 321,
			Description: "test_network_123_321"},
	})
}

// A typical lshw XML dump with lots of things left out.
const lshwXMLTestExtractInterfaces = `
<?xml version="1.0" standalone="yes" ?>
<!-- generated by lshw-B.02.16 -->
<list>
<node id="machine" claimed="true" class="system" handle="DMI:0001">
 <description>Notebook</description>
 <product>MyMachine</product>
 <version>1.0</version>
 <width units="bits">64</width>
  <node id="core" claimed="true" class="bus" handle="DMI:0002">
   <description>Motherboard</description>
    <node id="cpu" claimed="true" class="processor" handle="DMI:0004">
     <description>CPU</description>
      <node id="pci:2" claimed="true" class="bridge" handle="PCIBUS:0000:03">
        <node id="network" claimed="true" class="network" handle="PCI:0000:03:00.0">
         <logicalname>wlan0</logicalname>
         <serial>aa:bb:cc:dd:ee:ff</serial>
        </node>
        <node id="network" claimed="true" class="network" handle="PCI:0000:04:00.0">
         <logicalname>eth0</logicalname>
         <serial>aa:bb:cc:dd:ee:f1</serial>
        </node>
      </node>
    </node>
  </node>
  <node id="network:0" claimed="true" class="network" handle="">
   <logicalname>vnet1</logicalname>
   <serial>aa:bb:cc:dd:ee:f2</serial>
  </node>
</node>
</list>
`

func (suite *environSuite) TestExtractInterfaces(c *gc.C) {
	inst := suite.getInstance("testInstance")
	interfaces, err := extractInterfaces(inst, []byte(lshwXMLTestExtractInterfaces))
	c.Assert(err, gc.IsNil)
	c.Check(interfaces, jc.DeepEquals, map[string]string{
		"aa:bb:cc:dd:ee:ff": "wlan0",
		"aa:bb:cc:dd:ee:f1": "eth0",
		"aa:bb:cc:dd:ee:f2": "vnet1",
	})
}

func (suite *environSuite) TestGetInstanceNetworkInterfaces(c *gc.C) {
	inst := suite.getInstance("testInstance")
	templateInterfaces := map[string]string{
		"aa:bb:cc:dd:ee:ff": "wlan0",
		"aa:bb:cc:dd:ee:f1": "eth0",
		"aa:bb:cc:dd:ee:f2": "vnet1",
	}
	lshwXML, err := suite.generateHWTemplate(templateInterfaces)
	c.Assert(err, gc.IsNil)

	suite.testMAASObject.TestServer.AddNodeDetails("testInstance", lshwXML)
	interfaces, err := inst.environ.getInstanceNetworkInterfaces(inst)
	c.Assert(err, gc.IsNil)
	c.Check(interfaces, jc.DeepEquals, templateInterfaces)
}

func (suite *environSuite) TestSetupNetworks(c *gc.C) {
	test_instance := suite.getInstance("node1")
	templateInterfaces := map[string]string{
		"aa:bb:cc:dd:ee:ff": "wlan0",
		"aa:bb:cc:dd:ee:f1": "eth0",
		"aa:bb:cc:dd:ee:f2": "vnet1",
	}
	lshwXML, err := suite.generateHWTemplate(templateInterfaces)
	c.Assert(err, gc.IsNil)

	suite.testMAASObject.TestServer.AddNodeDetails("node1", lshwXML)
	suite.getNetwork("LAN", 2, 42)
	suite.testMAASObject.TestServer.ConnectNodeToNetworkWithMACAddress("node1", "LAN", "aa:bb:cc:dd:ee:f1")
	suite.getNetwork("Virt", 3, 0)
	suite.testMAASObject.TestServer.ConnectNodeToNetworkWithMACAddress("node1", "Virt", "aa:bb:cc:dd:ee:f2")
	suite.getNetwork("WLAN", 1, 0)
	suite.testMAASObject.TestServer.ConnectNodeToNetworkWithMACAddress("node1", "WLAN", "aa:bb:cc:dd:ee:ff")
	networkInfo, err := suite.makeEnviron().setupNetworks(test_instance, set.NewStrings("LAN", "Virt"))
	c.Assert(err, gc.IsNil)

	// Note: order of networks is based on lshwXML
	c.Check(networkInfo, jc.SameContents, []network.Info{
		network.Info{
			MACAddress:    "aa:bb:cc:dd:ee:ff",
			CIDR:          "192.168.1.1/24",
			NetworkName:   "WLAN",
			ProviderId:    "WLAN",
			VLANTag:       0,
			InterfaceName: "wlan0",
			Disabled:      true,
		},
		network.Info{
			MACAddress:    "aa:bb:cc:dd:ee:f1",
			CIDR:          "192.168.2.1/24",
			NetworkName:   "LAN",
			ProviderId:    "LAN",
			VLANTag:       42,
			InterfaceName: "eth0",
			Disabled:      false,
		},
		network.Info{
			MACAddress:    "aa:bb:cc:dd:ee:f2",
			CIDR:          "192.168.3.1/24",
			NetworkName:   "Virt",
			ProviderId:    "Virt",
			VLANTag:       0,
			InterfaceName: "vnet1",
			Disabled:      false,
		},
	})
}

// The same test, but now "Virt" network does not have matched MAC address
func (suite *environSuite) TestSetupNetworksPartialMatch(c *gc.C) {
	test_instance := suite.getInstance("node1")
	templateInterfaces := map[string]string{
		"aa:bb:cc:dd:ee:ff": "wlan0",
		"aa:bb:cc:dd:ee:f1": "eth0",
		"aa:bb:cc:dd:ee:f2": "vnet1",
	}
	lshwXML, err := suite.generateHWTemplate(templateInterfaces)
	c.Assert(err, gc.IsNil)

	suite.testMAASObject.TestServer.AddNodeDetails("node1", lshwXML)
	suite.getNetwork("LAN", 2, 42)
	suite.testMAASObject.TestServer.ConnectNodeToNetworkWithMACAddress("node1", "LAN", "aa:bb:cc:dd:ee:f1")
	suite.getNetwork("Virt", 3, 0)
	suite.testMAASObject.TestServer.ConnectNodeToNetworkWithMACAddress("node1", "Virt", "aa:bb:cc:dd:ee:f3")
	networkInfo, err := suite.makeEnviron().setupNetworks(test_instance, set.NewStrings("LAN"))
	c.Assert(err, gc.IsNil)

	// Note: order of networks is based on lshwXML
	c.Check(networkInfo, jc.SameContents, []network.Info{
		network.Info{
			MACAddress:    "aa:bb:cc:dd:ee:f1",
			CIDR:          "192.168.2.1/24",
			NetworkName:   "LAN",
			ProviderId:    "LAN",
			VLANTag:       42,
			InterfaceName: "eth0",
			Disabled:      false,
		},
	})
}

// The same test, but now no networks have matched MAC
func (suite *environSuite) TestSetupNetworksNoMatch(c *gc.C) {
	test_instance := suite.getInstance("node1")
	templateInterfaces := map[string]string{
		"aa:bb:cc:dd:ee:ff": "wlan0",
		"aa:bb:cc:dd:ee:f1": "eth0",
		"aa:bb:cc:dd:ee:f2": "vnet1",
	}
	lshwXML, err := suite.generateHWTemplate(templateInterfaces)
	c.Assert(err, gc.IsNil)

	suite.testMAASObject.TestServer.AddNodeDetails("node1", lshwXML)
	suite.getNetwork("Virt", 3, 0)
	suite.testMAASObject.TestServer.ConnectNodeToNetworkWithMACAddress("node1", "Virt", "aa:bb:cc:dd:ee:f3")
	networkInfo, err := suite.makeEnviron().setupNetworks(test_instance, set.NewStrings("Virt"))
	c.Assert(err, gc.IsNil)

	// Note: order of networks is based on lshwXML
	c.Check(networkInfo, gc.HasLen, 0)
}

func (suite *environSuite) TestSupportNetworks(c *gc.C) {
	env := suite.makeEnviron()
	c.Assert(env.SupportNetworks(), jc.IsTrue)
}
