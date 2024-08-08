package commandsummary

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jfrog/jfrog-cli-core/v2/utils/coreutils"
	"github.com/jfrog/jfrog-client-go/utils/errorutils"
	"github.com/jfrog/jfrog-client-go/utils/io/fileutils"
	"os"
	"path"
	"path/filepath"
	"strings"
)

type CommandSummaryInterface interface {
	GenerateMarkdownFromFiles(dataFilePaths []string) (finalMarkdown string, err error)
	GenerateSarifFromFiles(dataFilePaths []string) (finalSarif string, err error)
}

const (
	// The name of the directory where all the commands summaries will be stored.
	// Inside this directory, each command will have its own directory.
	OutputDirName = "jfrog-command-summary"
)

type CommandSummary struct {
	CommandSummaryInterface
	summaryOutputPath string
	commandsName      string
}

// Create a new instance of CommandSummary.
// Notice to check if the command should record the summary before calling this function.
// You can do this by calling the helper function ShouldRecordSummary.
func New(userImplementation CommandSummaryInterface, commandsName string) (cs *CommandSummary, err error) {
	outputDir := os.Getenv(coreutils.OutputDirPathEnv)
	if outputDir == "" {
		return nil, fmt.Errorf("output dir path is not defined, please set the JFROG_CLI_COMMAND_SUMMARY_OUTPUT_DIR environment variable")
	}
	cs = &CommandSummary{
		CommandSummaryInterface: userImplementation,
		commandsName:            commandsName,
		summaryOutputPath:       outputDir,
	}
	err = cs.prepareFileSystem()
	return
}

type generateFunc func([]string) (string, error)
type saveFunc func(string) error

func (cs *CommandSummary) record(data any, generate generateFunc, save saveFunc, prefix string) error {
	// TODO in what scenario there is more than one file here? Why need to save, then load?
	if err := cs.saveDataToFileSystem(data, prefix); err != nil {
		return err
	}
	dataFilesPaths, err := cs.getAllDataFilesPaths()
	if err != nil {
		return fmt.Errorf("failed to load data files from directory %s, with error: %w", cs.commandsName, err)
	}

	// TODO [Error] failed to render markdown: unexpected end of JSON input - what is the cause?
	content, err := generate(dataFilesPaths)
	if err != nil {
		return fmt.Errorf("failed to generate content: %w", err)
	}

	if err = save(content); err != nil {
		return fmt.Errorf("failed to save content to file system: %w", err)
	}
	return nil
}

func (cs *CommandSummary) RecordSarif(data any) error {
	return cs.record(data, cs.GenerateSarifFromFiles, cs.saveSarifToFileSystem, "sarif")
}

func (cs *CommandSummary) RecordMarkdown(data any) error {
	return cs.record(data, cs.GenerateMarkdownFromFiles, cs.saveMarkdownToFileSystem, "markdown")
}

func (cs *CommandSummary) getAllDataFilesPaths() ([]string, error) {
	entries, err := os.ReadDir(cs.summaryOutputPath)
	if err != nil {
		return nil, errorutils.CheckError(err)
	}
	// Exclude markdown files
	var filePaths []string
	for _, entry := range entries {
		if !entry.IsDir() && !strings.HasSuffix(entry.Name(), ".md") {
			filePaths = append(filePaths, path.Join(cs.summaryOutputPath, entry.Name()))
		}
	}
	return filePaths, nil
}

// TODO does the file name matter?
func (cs *CommandSummary) saveSarifToFileSystem(sarif string) (err error) {
	return cs.saveFormatToFileSystem(sarif, "sarif")
}

// TODO lock because it might be multi threaded
func (cs *CommandSummary) saveMarkdownToFileSystem(markdown string) (err error) {
	return cs.saveFormatToFileSystem(markdown, "markdown.md")
}

func (cs *CommandSummary) saveFormatToFileSystem(content, fileName string) (err error) {
	file, err := os.OpenFile(path.Join(cs.summaryOutputPath, fileName), os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return errorutils.CheckError(err)
	}
	defer func() {
		err = errors.Join(err, errorutils.CheckError(file.Close()))
	}()
	if _, err = file.WriteString(content); err != nil {
		return errorutils.CheckError(err)
	}
	return
}

// Saves the given data into a file in the specified directory.
func (cs *CommandSummary) saveDataToFileSystem(data interface{}, prefix string) error {
	// Create a random file name in the data file path.
	fd, err := os.CreateTemp(cs.summaryOutputPath, prefix+"-data-*")
	if err != nil {
		return errorutils.CheckError(err)
	}
	defer func() {
		err = errors.Join(err, fd.Close())
	}()

	// Convert the data into bytes.
	bytes, err := convertDataToBytes(data)
	if err != nil {
		return errorutils.CheckError(err)
	}

	// Write the bytes to the file.
	if _, err = fd.Write(bytes); err != nil {
		return errorutils.CheckError(err)
	}

	return nil
}

// This function creates the base dir for the command summary inside
// the path the user has provided, userPath/OutputDirName.
// Then it creates a specific directory for the command, path/OutputDirName/commandsName.
// And set the summaryOutputPath to the specific command directory.
func (cs *CommandSummary) prepareFileSystem() (err error) {
	summaryBaseDirPath := filepath.Join(cs.summaryOutputPath, OutputDirName)
	if err = createDirIfNotExists(summaryBaseDirPath); err != nil {
		return err
	}
	specificCommandOutputPath := filepath.Join(summaryBaseDirPath, cs.commandsName)
	if err = createDirIfNotExists(specificCommandOutputPath); err != nil {
		return err
	}
	// Sets the specific command output path
	cs.summaryOutputPath = specificCommandOutputPath
	return
}

// If the output dir path is not defined, the command summary should not be recorded.
func ShouldRecordSummary() bool {
	return os.Getenv(coreutils.OutputDirPathEnv) != ""
}

// Helper function to unmarshal data from a file path into the target object.
func UnmarshalFromFilePath(dataFile string, target any) (err error) {
	data, err := fileutils.ReadFile(dataFile)
	if err != nil {
		return
	}
	if err = json.Unmarshal(data, target); err != nil {
		return errorutils.CheckError(err)
	}
	return
}

// Converts the given data into a byte array.
// Handle specific conversion cases
func convertDataToBytes(data interface{}) ([]byte, error) {
	switch v := data.(type) {
	case []byte:
		return v, nil
	default:
		content, err := json.Marshal(data)
		return content, errorutils.CheckError(err)
	}
}

func createDirIfNotExists(homeDir string) error {
	return errorutils.CheckError(os.MkdirAll(homeDir, 0755))
}
