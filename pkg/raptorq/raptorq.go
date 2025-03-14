package raptorq

import (
	"github.com/LumeraProtocol/supernode/pkg/errors"
	"io/fs"
	"io/ioutil"
	"os"
	"path/filepath"

	json "github.com/json-iterator/go"

	"github.com/google/uuid"

	pb "github.com/LumeraProtocol/supernode/gen/raptorq"
)

const (
	inputEncodeFileName = "input.data"
	symbolIDFileSubDir  = "meta"
	symbolFileSubdir    = "symbols"
	concurrency         = 1
)

type RawSymbolIDFile struct {
	ID                string   `json:"id"`
	BlockHash         string   `json:"block_hash"`
	PastelID          string   `json:"pastel_id"`
	SymbolIdentifiers []string `json:"symbol_identifiers"`
}

type raptorQ struct {
	conn      *clientConn
	client    pb.RaptorQClient
	config    Config
	semaphore chan struct{} // Semaphore to control concurrency
}

func randID() string {
	id := uuid.NewString()
	return id[0:8]
}

func writeFile(path string, data []byte) error {
	return ioutil.WriteFile(path, data, 0750)
}

func readFile(path string) ([]byte, error) {
	return ioutil.ReadFile(path)
}

func createTaskFolder(base string, subDirs ...string) (string, error) {
	taskID := randID()
	taskPath := filepath.Join(base, taskID)
	taskPath = filepath.Join(taskPath, filepath.Join(subDirs...))

	err := os.MkdirAll(taskPath, 0750)

	if err != nil {
		return "", err
	}

	return taskPath, nil
}

func createInputEncodeFile(base string, data []byte) (taskPath string, inputFile string, err error) {
	taskPath, err = createTaskFolder(base)

	if err != nil {
		return "", "", errors.Errorf("create task folder: %w", err)
	}

	inputFile = filepath.Join(taskPath, inputEncodeFileName)
	err = writeFile(inputFile, data)

	if err != nil {
		return "", "", errors.Errorf("write file: %w", err)
	}

	return taskPath, inputFile, nil
}

func createInputDecodeSymbols(base string, symbols map[string][]byte) (path string, err error) {
	path, err = createTaskFolder(base, symbolFileSubdir)

	if err != nil {
		return "", errors.Errorf("create task folder: %w", err)
	}

	for id, data := range symbols {
		symbolFile := filepath.Join(path, id)
		err = writeFile(symbolFile, data)

		if err != nil {
			return "", errors.Errorf("write symbol file: %w", err)
		}
	}

	return path, nil
}

// scan symbol id files in "meta" folder, return map of file Ids & contents of file (as list of line)
func scanSymbolIDFiles(dirPath string) (map[string]RawSymbolIDFile, error) {
	filesMap := make(map[string]RawSymbolIDFile)

	err := filepath.Walk(dirPath, func(path string, info fs.FileInfo, err error) error {
		if err != nil {
			return errors.Errorf("scan a path %s: %w", path, err)
		}

		if info.IsDir() {
			// TODO - compare it to root
			return nil
		}

		fileID := filepath.Base(path)

		configFile, err := os.Open(path)
		if err != nil {
			return errors.Errorf("opening file: %s - err: %w", path, err)
		}
		defer configFile.Close()

		file := RawSymbolIDFile{}
		jsonParser := json.NewDecoder(configFile)
		if err = jsonParser.Decode(&file); err != nil {
			return errors.Errorf("parsing file: %s - err: %w", path, err)
		}

		filesMap[fileID] = file

		return nil
	})

	if err != nil {
		return nil, err
	}

	return filesMap, nil
}
