package commands

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/codegangsta/cli"
	"github.com/fatih/color"
	"github.com/leancloud/lean-cli/api"
	"github.com/leancloud/lean-cli/apps"
)

func logsAction(c *cli.Context) error {
	follow := c.Bool("f")
	env := c.String("e")
	limit := c.Int("limit")
	format := c.String("format")
	isProd := false

	if env == "staging" || env == "stag" {
		isProd = false
	} else if env == "production" || env == "" || env == "prod" {
		isProd = true
	} else {
		return cli.NewExitError("environment 参数必须为 staging 或者 production", 1)
	}

	appID, err := apps.GetCurrentAppID("")
	if err == apps.ErrNoAppLinked {
		return cli.NewExitError("没有关联任何 app，请使用 lean checkout 来关联应用。", 1)
	}
	if err != nil {
		return newCliError(err)
	}
	info, err := api.GetAppInfo(appID)
	if err != nil {
		return newCliError(err)
	}

	var printer api.LogPrinter
	if format == "default" {
		printer = getDefaultLogPrinter(isProd)
	} else if strings.ToLower(format) == "json" {
		printer = jsonLogPrinter
	} else {
		return cli.NewExitError("错误的 format 参数，必须为 json / default 其中之一。", 1)
	}
	if err := api.PrintLogsByLimit(printer, info.AppID, info.MasterKey, follow, isProd, limit); err != nil {
		return err
	}

	return nil
}

func getDefaultLogPrinter(isProd bool) api.LogPrinter {
	// 根据文档描述，有些类型的日志中的 production 字段，不论生产环境还是预备环境都会为 1，因此不能以此字段
	// 为依据来决定展示样式。
	return func(log *api.Log) error {
		t, err := time.Parse(time.RFC3339, log.Time)
		if err != nil {
			return err
		}
		content := strings.TrimSuffix(log.Content, "\n")
		level := log.Level
		var levelSprintf func(string, ...interface{}) string
		if level == "info" {
			levelSprintf = color.New(color.BgGreen, color.FgWhite).SprintfFunc()
		} else {
			levelSprintf = color.New(color.BgRed, color.FgWhite).SprintfFunc()
		}
		var instance string
		if log.Instance == "" {
			instance = "    "
		} else {
			instance = log.Instance
		}

		if isProd {
			fmt.Fprintf(color.Output, "%s %s %s\r\n", instance, levelSprintf(" %s ", t.Local().Format("15:04:05")), content)
		} else {
			// no instance column
			fmt.Fprintf(color.Output, "%s %s\r\n", levelSprintf(" %s ", t.Local().Format("15:04:05")), content)
		}

		return nil
	}
}

func jsonLogPrinter(log *api.Log) error {
	content, err := json.Marshal(log)
	if err != nil {
		return err
	}
	fmt.Println(string(content))
	return nil
}
