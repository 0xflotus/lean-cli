package runtimes

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/aisk/logp"
	"github.com/facebookgo/parseignore"
	"github.com/facebookgo/symwalk"
	"github.com/leancloud/lean-cli/utils"
)

var ErrRuntimeNotFound = errors.New("Unsupported project structure. Please inspect your directory structure to make sure it is a valid LeanEngine project.")

type filesPattern struct {
	Includes []string
	Excludes []string
}

// Runtime stands for a language runtime
type Runtime struct {
	command     *exec.Cmd
	WorkDir     string
	ProjectPath string
	Name        string
	Exec        string
	Args        []string
	Envs        []string
	Remote      string
	Port        string
	// DeployFiles is the patterns for source code to deploy to the remote server
	DeployFiles filesPattern
	// Errors is the channel that receives the command's error result
	Errors chan error
}

// Run the project, and watch file changes
func (runtime *Runtime) Run() {
	go func() {
		runtime.command = exec.Command(runtime.Exec, runtime.Args...)
		runtime.command.Dir = runtime.WorkDir
		runtime.command.Stdout = os.Stdout
		runtime.command.Stderr = os.Stderr
		runtime.command.Env = os.Environ()

		for _, env := range runtime.Envs {
			runtime.command.Env = append(runtime.command.Env, env)
		}

		logp.Infof("Use %s to start the project\r\n", runtime.command.Args)
		logp.Infof("The project is running at: http://localhost:%s\r\n", runtime.Port)
		err := runtime.command.Run()
		if err != nil {
			runtime.Errors <- err
		}
	}()
}

func (runtime *Runtime) ArchiveUploadFiles(archiveFile string, ignoreFilePath string) error {
	return runtime.defaultArchive(archiveFile, ignoreFilePath)
}

func (runtime *Runtime) defaultArchive(archiveFile string, ignoreFilePath string) error {
	matcher, err := runtime.readIgnore(ignoreFilePath)
	if os.IsNotExist(err) {
		return fmt.Errorf("The designated ignore file '%s' doesn't exist", ignoreFilePath)
	} else if err != nil {
		return err
	}

	files := []struct{ Name, Path string }{}
	err = symwalk.Walk(".", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		// convert DOS's '\' path seprater to UNIX style
		path = filepath.ToSlash(path)
		decision, err := matcher.Match(path, info)
		if err != nil {
			return err
		}

		if info.IsDir() {
			if decision == parseignore.Exclude {
				return filepath.SkipDir
			}
			return nil
		}

		if decision != parseignore.Exclude {
			files = append(files, struct{ Name, Path string }{
				Name: path,
				Path: path,
			})
		}
		return nil
	})

	if err != nil {
		return err
	}

	return utils.ArchiveFiles(archiveFile, files)
}

// DetectRuntime returns the project's runtime
func DetectRuntime(projectPath string) (*Runtime, error) {
	// order is important
	if utils.IsFileExists(filepath.Join(projectPath, "cloud", "main.js")) {
		logp.Info("cloudcode runtime detected")
		return &Runtime{
			Name: "cloudcode",
		}, nil
	}
	packageFilePath := filepath.Join(projectPath, "package.json")
	if utils.IsFileExists(filepath.Join(projectPath, "server.js")) && utils.IsFileExists(packageFilePath) {
		logp.Info("Node.js runtime detected")
		return newNodeRuntime(projectPath)
	}
	if utils.IsFileExists(packageFilePath) {
		data, err := ioutil.ReadFile(packageFilePath)
		if err == nil {
			data = utils.StripUTF8BOM(data)
			var result struct {
				Scripts struct {
					Start string `json:"start"`
				} `json:"scripts"`
			}
			if err = json.Unmarshal(data, &result); err == nil {
				if result.Scripts.Start != "" {
					logp.Info("Node.js runtime detected")
					return newNodeRuntime(projectPath)
				}
			}
		}
	}
	if utils.IsFileExists(filepath.Join(projectPath, "requirements.txt")) && utils.IsFileExists(filepath.Join(projectPath, "wsgi.py")) {
		logp.Info("Python runtime detected")
		return newPythonRuntime(projectPath)
	}
	if utils.IsFileExists(filepath.Join(projectPath, "composer.json")) && utils.IsFileExists(filepath.Join("public", "index.php")) {
		logp.Info("PHP runtime detected")
		return newPhpRuntime(projectPath)
	}
	if utils.IsFileExists(filepath.Join(projectPath, "pom.xml")) {
		logp.Info("Java runtime detected")
		return newJavaRuntime(projectPath)
	}
	if utils.IsFileExists(filepath.Join(projectPath, "app.sln")) {
		logp.Info("DotNet runtime detected")
		return newDotnetRuntime(projectPath)
	}

	return &Runtime{
		ProjectPath: projectPath,
		Name:        "Unknown",
		Errors:      make(chan error),
	}, ErrRuntimeNotFound
}

func lookupBin(fallbacks []string) (string, error) {
	for i, bin := range fallbacks {
		binPath, err := exec.LookPath(bin)
		if err == nil { // found
			if i == 0 {
				logp.Infof("Found executable file: `%s`\r\n", binPath)
			} else {
				logp.Warnf("Cannot find command `%s`, using `%s` instead of \r\n", fallbacks[i-1], fallbacks[i])
			}
			return bin, nil
		}
	}

	return "", fmt.Errorf("`%s` not found", fallbacks[0])
}

func newPythonRuntime(projectPath string) (*Runtime, error) {
	// TODO: reafactor this shit
	if config, err := getEngineConfig(projectPath); err == nil {
		if config.CMD != "" {
			return newPythonRuntimeFromEngineConfig(projectPath, config)
		}
	}
	content, err := ioutil.ReadFile(filepath.Join(projectPath, ".python-version"))
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, err
		}

		execName := "python2.7"
		content, err = ioutil.ReadFile(filepath.Join(projectPath, "runtime.txt"))
		if err != nil {
			if !os.IsNotExist(err) {
				return nil, err
			}
			// the default content
			content = []byte("python-2.7")
		}
		if strings.HasPrefix(string(content), "python-2.7") {
			execName, err = lookupBin([]string{"python2.7", "python2", "python"})
			if err != nil {
				return nil, err
			}
		} else if strings.HasPrefix(string(content), "python-3.5") {
			execName, err = lookupBin([]string{"python3.5", "python3", "python"})
			if err != nil {
				return nil, err
			}
		} else {
			return nil, errors.New("invalid python runtime.txt format, only `python-2.7` and `python-3.5` were allowed")
		}

		return &Runtime{
			ProjectPath: projectPath,
			Name:        "python",
			Exec:        execName,
			Args:        []string{"wsgi.py"},
			Errors:      make(chan error),
		}, nil
	}
	pythonVersion := string(content)
	if !(strings.HasPrefix(pythonVersion, "2.") || strings.HasPrefix(pythonVersion, "3.")) {
		return nil, errors.New("Wrong pyenv version. We only support CPython. Please check and correct .python-version")
	}
	logp.Info("pyenv detected. Please make sure pyenv is configured properly.")

	return &Runtime{
		ProjectPath: projectPath,
		Name:        "python",
		Exec:        "python",
		Args:        []string{"wsgi.py"},
		Errors:      make(chan error),
	}, nil
}

func newPythonRuntimeFromEngineConfig(projectPath string, config *engineConfig) (*Runtime, error) {
	exec, args := config.parseCMD()
	return &Runtime{
		ProjectPath: projectPath,
		Name:        "python",
		Exec:        exec,
		Args:        args,
		Errors:      make(chan error),
	}, nil

}

func newNodeRuntime(projectPath string) (*Runtime, error) {
	execName := "node"
	args := []string{"server.js"}
	pkgFile := filepath.Join(projectPath, "package.json")
	if content, err := ioutil.ReadFile(pkgFile); err == nil {
		content = utils.StripUTF8BOM(content)
		pkg := new(struct {
			Scripts struct {
				Start string `json:"start"`
				Dev   string `json:"dev"`
			} `json:"scripts"`
			Dependencies map[string]string `json:"dependencies"`
		})
		err = json.Unmarshal(content, pkg)
		if err != nil {
			return nil, err
		}

		if pkg.Scripts.Dev != "" {
			execName = "npm"
			args = []string{"run", "dev"}
		} else if pkg.Scripts.Start != "" {
			execName = "npm"
			args = []string{"start"}
		}

		if sdkVersion, ok := pkg.Dependencies["leanengine"]; ok {
			if strings.HasPrefix(sdkVersion, "0.") ||
				strings.HasPrefix(sdkVersion, "~0.") ||
				strings.HasPrefix(sdkVersion, "^0.") {
				logp.Warn("The current leanengine SDK is too low. Local debugging of cloud functions is not supported. Please refer to http://url.leanapp.cn/Og1cVia for upgrade instructions")
			}
		}

	}

	return &Runtime{
		ProjectPath: projectPath,
		Name:        "node.js",
		Exec:        execName,
		Args:        args,
		Errors:      make(chan error),
	}, nil
}

func newJavaRuntime(projectPath string) (*Runtime, error) {
	exec := "mvn"
	args := []string{"jetty:run"}
	if config, err := getEngineConfig(projectPath); err == nil {
		if config.CMD != "" {
			exec, args = config.parseCMD()
		}
	}
	return &Runtime{
		ProjectPath: projectPath,
		Name:        "java",
		Exec:        exec,
		Args:        args,
		Errors:      make(chan error),
	}, nil
}

func newPhpRuntime(projectPath string) (*Runtime, error) {
	entryScript, err := getPHPEntryScriptPath()
	if err != nil {
		return nil, err
	}
	return &Runtime{
		ProjectPath: projectPath,
		Name:        "php",
		Exec:        "php",
		Args:        []string{"-S", "127.0.0.1:3000", "-t", "public", entryScript},
		Errors:      make(chan error),
	}, nil
}

func newDotnetRuntime(projectPath string) (*Runtime, error) {
	return &Runtime{
		WorkDir:     filepath.Join(projectPath, "web"),
		ProjectPath: projectPath,
		Name:        "dotnet",
		Exec:        "dotnet",
		Args:        []string{"run"},
		Envs:        []string{"ASPNETCORE_URLS=http://0.0.0.0:3000"},
		Errors:      make(chan error),
	}, nil
}
