package npm

import (
	"bufio"
	"errors"
	"fmt"
	commandUtils "github.com/jfrog/jfrog-cli-core/v2/artifactory/commands/utils"
	"github.com/jfrog/jfrog-cli-core/v2/artifactory/utils/npm"
	"github.com/jfrog/jfrog-cli-core/v2/utils/coreutils"
	npmutils "github.com/jfrog/jfrog-cli-core/v2/utils/npm"
	"github.com/jfrog/jfrog-client-go/auth"
	"github.com/jfrog/jfrog-client-go/utils/errorutils"
	"github.com/jfrog/jfrog-client-go/utils/log"
	"github.com/jfrog/jfrog-client-go/utils/version"
	"io/ioutil"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

type CommonArgs struct {
	cmdName          string
	jsonOutput       bool
	executablePath   string
	restoreNpmrcFunc func() error
	workingDirectory string
	registry         string
	npmAuth          string
	collectBuildInfo bool
	typeRestriction  npmutils.TypeRestriction
	authArtDetails   auth.ServiceDetails
	npmVersion       *version.Version
	packageInfo      *npmutils.PackageInfo
	NpmCommand
}

func (com *CommonArgs) preparePrerequisites(repo string) error {
	log.Debug("Preparing prerequisites.")
	npmExecPath, err := exec.LookPath("npm")
	if err != nil {
		return errorutils.CheckError(err)
	}

	if npmExecPath == "" {
		return errorutils.CheckError(errors.New("could not find the 'npm' executable in the system PATH"))
	}
	com.executablePath = npmExecPath

	if err = com.validateNpmVersion(); err != nil {
		return err
	}

	if err := com.setJsonOutput(); err != nil {
		return err
	}

	com.workingDirectory, err = coreutils.GetWorkingDirectory()
	if err != nil {
		return err
	}
	log.Debug("Working directory set to:", com.workingDirectory)

	if err = com.setArtifactoryAuth(); err != nil {
		return err
	}

	com.npmAuth, com.registry, err = commandUtils.GetArtifactoryNpmRepoDetails(repo, &com.authArtDetails)
	if err != nil {
		return err
	}

	com.collectBuildInfo, com.packageInfo, err = commandUtils.PrepareBuildInfo(com.workingDirectory, com.buildConfiguration, com.npmVersion)
	if err != nil {
		return err
	}

	com.restoreNpmrcFunc, err = commandUtils.BackupFile(filepath.Join(com.workingDirectory, npmrcFileName), filepath.Join(com.workingDirectory, npmrcBackupFileName))
	return err
}

func (com *CommonArgs) setJsonOutput() error {
	jsonOutput, err := npm.ConfigGet(com.npmArgs, "json", com.executablePath)
	if err != nil {
		return err
	}

	// In case of --json=<not boolean>, the value of json is set to 'true', but the result from the command is not 'true'
	com.jsonOutput = jsonOutput != "false"
	return nil
}

func (com *CommonArgs) setArtifactoryAuth() error {
	authArtDetails, err := com.serverDetails.CreateArtAuthConfig()
	if err != nil {
		return err
	}
	if authArtDetails.GetSshAuthHeaders() != nil {
		return errorutils.CheckError(errors.New("SSH authentication is not supported in this command"))
	}
	com.authArtDetails = authArtDetails
	return nil
}

// In order to make sure the npm resolves artifacts from Artifactory we create a .npmrc file in the project dir.
// If such a file exists we back it up as npmrcBackupFileName.
func (com *CommonArgs) createTempNpmrc() error {
	log.Debug("Creating project .npmrc file.")
	data, err := npm.GetConfigList(com.npmArgs, com.executablePath)
	configData, err := com.prepareConfigData(data)
	if err != nil {
		return errorutils.CheckError(err)
	}

	if err = removeNpmrcIfExists(com.workingDirectory); err != nil {
		return err
	}

	return errorutils.CheckError(ioutil.WriteFile(filepath.Join(com.workingDirectory, npmrcFileName), configData, 0600))
}

func (com *CommonArgs) setTypeRestriction(key string, value string) {
	// From npm 7, type restriction is determined by 'omit' and 'include' (both appear in 'npm config ls').
	// Other options (like 'dev', 'production' and 'only') are deprecated, but if they're used anyway - 'omit' and 'include' are automatically calculated.
	// So 'omit' is always preferred, if it exists.
	if key == "omit" {
		if strings.Contains(value, "dev") {
			com.typeRestriction = npmutils.ProdOnly
		} else {
			com.typeRestriction = npmutils.All
		}
	} else if com.typeRestriction == npmutils.DefaultRestriction { // Until npm 6, configurations in 'npm config ls' are sorted by priority in descending order, so typeRestriction should be set only if it was not set before
		if key == "only" {
			if strings.Contains(value, "prod") {
				com.typeRestriction = npmutils.ProdOnly
			} else if strings.Contains(value, "dev") {
				com.typeRestriction = npmutils.DevOnly
			}
		} else if key == "production" && strings.Contains(value, "true") {
			com.typeRestriction = npmutils.ProdOnly
		}
	}
}

func (com *CommonArgs) restoreNpmrcAndError(err error) error {
	if restoreErr := com.restoreNpmrcFunc(); restoreErr != nil {
		return errorutils.CheckError(errors.New(fmt.Sprintf("Two errors occurred:\n %s\n %s", restoreErr.Error(), err.Error())))
	}
	return err
}

func (com *CommonArgs) validateNpmVersion() error {
	npmVersion, err := npmutils.Version(com.executablePath)
	if err != nil {
		return err
	}
	if npmVersion.Compare(minSupportedNpmVersion) > 0 {
		return errorutils.CheckError(errors.New(fmt.Sprintf(
			"JFrog CLI npm %s command requires npm client version "+minSupportedNpmVersion+" or higher. The Current version is: %s", com.cmdName, npmVersion.GetVersion())))
	}
	com.npmVersion = npmVersion
	return nil
}

// This func transforms "npm config list" result to key=val list of values that can be set to .npmrc file.
// it filters any nil values key, changes registry and scope registries to Artifactory url and adds Artifactory authentication to the list
func (com *CommonArgs) prepareConfigData(data []byte) ([]byte, error) {
	var filteredConf []string
	configString := string(data)
	scanner := bufio.NewScanner(strings.NewReader(configString))

	for scanner.Scan() {
		currOption := scanner.Text()
		if currOption != "" {
			splitOption := strings.SplitN(currOption, "=", 2)
			key := strings.TrimSpace(splitOption[0])
			if len(splitOption) == 2 && isValidKey(key) {
				value := strings.TrimSpace(splitOption[1])
				if strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]") {
					filteredConf = addArrayConfigs(filteredConf, key, value)
				} else {
					filteredConf = append(filteredConf, currOption, "\n")
				}
				com.setTypeRestriction(key, value)
			} else if strings.HasPrefix(splitOption[0], "@") {
				// Override scoped registries (@scope = xyz)
				filteredConf = append(filteredConf, splitOption[0], " = ", com.registry, "\n")
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, errorutils.CheckError(err)
	}

	filteredConf = append(filteredConf, "json = ", strconv.FormatBool(com.jsonOutput), "\n")
	filteredConf = append(filteredConf, "registry = ", com.registry, "\n")
	filteredConf = append(filteredConf, com.npmAuth)
	return []byte(strings.Join(filteredConf, "")), nil
}
