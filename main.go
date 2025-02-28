package main

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/docopt/docopt-go"
	"github.com/kovetskiy/lorg"
	"github.com/bonovoxly/mark/pkg/confluence"
	"github.com/bonovoxly/mark/pkg/mark"
	"github.com/bonovoxly/mark/pkg/mark/includes"
	"github.com/bonovoxly/mark/pkg/mark/macro"
	"github.com/bonovoxly/mark/pkg/mark/stdlib"
	"github.com/reconquest/karma-go"
	"github.com/reconquest/pkg/log"
)

type Flags struct {
	FileGlobPatten string `docopt:"-f"`
	CompileOnly    bool   `docopt:"--compile-only"`
	DryRun         bool   `docopt:"--dry-run"`
	EditLock       bool   `docopt:"-k"`
	DropH1         bool   `docopt:"--drop-h1"`
	MinorEdit      bool   `docopt:"--minor-edit"`
	Color          string `docopt:"--color"`
	Debug          bool   `docopt:"--debug"`
	Trace          bool   `docopt:"--trace"`
	Username       string `docopt:"-u"`
	Password       string `docopt:"-p"`
	TargetURL      string `docopt:"-l"`
	BaseURL        string `docopt:"--base-url"`
}

const (
	version = "5.7"
	usage   = `mark - a tool for updating Atlassian Confluence pages from markdown.

Docs: https://github.com/bonovoxly/mark

Usage:
  mark [options] [-u <username>] [-p <token>] [-k] [-l <url>] -f <file>
  mark [options] [-u <username>] [-p <password>] [-k] [-b <url>] -f <file>
  mark -v | --version
  mark -h | --help

Options:
  -u <username>        Use specified username for updating Confluence page.
  -p <token>           Use specified token for updating Confluence page.
                        Specify - as password to read password from stdin.
  -l <url>             Edit specified Confluence page.
                        If -l is not specified, file should contain metadata (see
                        above).
  -b --base-url <url>  Base URL for Confluence.
                        Alternative option for base_url config field.
  -f <file>            Use specified markdown file(s) for converting to html. Supports file globbing patterns (needs to be quoted).
  -k                   Lock page editing to current user only to prevent accidental
                        manual edits over Confluence Web UI.
  --drop-h1            Don't include H1 headings in Confluence output.
  --dry-run            Resolve page and ancestry, show resulting HTML and exit.
  --compile-only       Show resulting HTML and don't update Confluence page content.
  --minor-edit         Don't send notifications while updating Confluence page.
  --debug              Enable debug logs.
  --trace              Enable trace logs.
  --color <when>       Display logs in color. Possible values: auto, never.
                        [default: auto]
  -h --help            Show this screen and call 911.
  -v --version         Show version.
`
)

func main() {
	cmd, err := docopt.ParseArgs(usage, nil, version)
	if err != nil {
		panic(err)
	}

	var flags Flags
	err = cmd.Bind(&flags)
	if err != nil {
		log.Fatal(err)
	}

	if flags.Debug {
		log.SetLevel(lorg.LevelDebug)
	}

	if flags.Trace {
		log.SetLevel(lorg.LevelTrace)
	}

	if flags.Color == "never" {
		log.GetLogger().SetFormat(
			lorg.NewFormat(
				`${time:2006-01-02 15:04:05.000} ${level:%s:left:true} ${prefix}%s`,
			),
		)
		log.GetLogger().SetOutput(os.Stderr)
	}

	config, err := LoadConfig(filepath.Join(os.Getenv("HOME"), ".config/mark"))
	if err != nil {
		log.Fatal(err)
	}

	creds, err := GetCredentials(flags, config)
	if err != nil {
		log.Fatal(err)
	}

	api := confluence.NewAPI(creds.BaseURL, creds.Username, creds.Password)

	files, err := filepath.Glob(flags.FileGlobPatten)
	if err != nil {
		log.Fatal(err)
	}
	if len(files) == 0 {
		log.Fatal("No files matched")
	}

	// Loop through files matched by glob pattern
	for _, file := range files {
		log.Infof(
			nil,
			"processing %s",
			file,
		)

		target := processFile(file, api, flags, creds.PageID, creds.Username)

		log.Infof(
			nil,
			"page successfully updated: %s",
			creds.BaseURL+target.Links.Full,
		)

		fmt.Println(creds.BaseURL + target.Links.Full)
	}
}

func processFile(
	file string,
	api *confluence.API,
	flags Flags,
	pageID string,
	username string,
) *confluence.PageInfo {
	markdown, err := ioutil.ReadFile(file)
	if err != nil {
		log.Fatal(err)
	}

	meta, markdown, err := mark.ExtractMeta(markdown)
	if err != nil {
		log.Fatal(err)
	}

	stdlib, err := stdlib.New(api)
	if err != nil {
		log.Fatal(err)
	}

	templates := stdlib.Templates

	var recurse bool

	for {
		templates, markdown, recurse, err = includes.ProcessIncludes(
			markdown,
			templates,
		)
		if err != nil {
			log.Fatal(err)
		}

		if !recurse {
			break
		}
	}

	macros, markdown, err := macro.ExtractMacros(markdown, templates)
	if err != nil {
		log.Fatal(err)
	}

	macros = append(macros, stdlib.Macros...)

	for _, macro := range macros {
		markdown, err = macro.Apply(markdown)
		if err != nil {
			log.Fatal(err)
		}
	}

	links, err := mark.ResolveRelativeLinks(api, meta, markdown, ".")
	if err != nil {
		log.Fatalf(err, "unable to resolve relative links")
	}

	markdown = mark.SubstituteLinks(markdown, links)

	if flags.DryRun {
		flags.CompileOnly = true

		_, _, err := mark.ResolvePage(flags.DryRun, api, meta)
		if err != nil {
			log.Fatalf(err, "unable to resolve page location")
		}
	}

	if flags.CompileOnly {
		fmt.Println(mark.CompileMarkdown(markdown, stdlib))
		os.Exit(0)
	}

	if pageID != "" && meta != nil {
		log.Warning(
			`specified file contains metadata, ` +
				`but it will be ignored due specified command line URL`,
		)

		meta = nil
	}

	if pageID == "" && meta == nil {
		log.Fatal(
			`specified file doesn't contain metadata ` +
				`and URL is not specified via command line ` +
				`or doesn't contain pageId GET-parameter`,
		)
	}

	var target *confluence.PageInfo

	if meta != nil {
		parent, page, err := mark.ResolvePage(flags.DryRun, api, meta)
		if err != nil {
			log.Fatalf(
				karma.Describe("title", meta.Title).Reason(err),
				"unable to resolve %s",
				meta.Type,
			)
		}

		if page == nil {
			page, err = api.CreatePage(
				meta.Space,
				meta.Type,
				parent,
				meta.Title,
				``,
			)
			if err != nil {
				log.Fatalf(
					err,
					"can't create %s %q",
					meta.Type,
					meta.Title,
				)
			}
		}

		target = page
	} else {
		if pageID == "" {
			log.Fatalf(nil, "URL should provide 'pageId' GET-parameter")
		}

		page, err := api.GetPageByID(pageID)
		if err != nil {
			log.Fatalf(err, "unable to retrieve page by id")
		}

		target = page
	}

	attaches, err := mark.ResolveAttachments(api, target, ".", meta.Attachments)
	if err != nil {
		log.Fatalf(err, "unable to create/update attachments")
	}

	markdown = mark.CompileAttachmentLinks(markdown, attaches)

	if flags.DropH1 {
		log.Info(
			"the leading H1 heading will be excluded from the Confluence output",
		)
		markdown = mark.DropDocumentLeadingH1(markdown)
	}

	html := mark.CompileMarkdown(markdown, stdlib)

	{
		var buffer bytes.Buffer

		err := stdlib.Templates.ExecuteTemplate(
			&buffer,
			"ac:layout",
			struct {
				Layout string
				Body   string
			}{
				Layout: meta.Layout,
				Body:   html,
			},
		)
		if err != nil {
			log.Fatal(err)
		}

		html = buffer.String()
	}

	err = api.UpdatePage(target, html, flags.MinorEdit, meta.Labels)
	if err != nil {
		log.Fatal(err)
	}

	if flags.EditLock {
		log.Infof(
			nil,
			`edit locked on page %q by user %q to prevent manual edits`,
			target.Title,
			username,
		)

		err := api.RestrictPageUpdates(target, username)
		if err != nil {
			log.Fatal(err)
		}
	}

	return target
}
