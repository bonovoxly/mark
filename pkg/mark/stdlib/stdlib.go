package stdlib

import (
	"strings"
	"text/template"

	"github.com/bonovoxly/mark/pkg/confluence"
	"github.com/bonovoxly/mark/pkg/mark/macro"
	"github.com/reconquest/pkg/log"

	"github.com/reconquest/karma-go"
)

type Lib struct {
	Macros    []macro.Macro
	Templates *template.Template
}

func New(api *confluence.API) (*Lib, error) {
	var (
		lib Lib
		err error
	)

	lib.Templates, err = templates(api)
	if err != nil {
		return nil, err
	}

	lib.Macros, err = macros(lib.Templates)
	if err != nil {
		return nil, err
	}

	return &lib, nil
}

func macros(templates *template.Template) ([]macro.Macro, error) {
	text := func(line ...string) []byte {
		return []byte(strings.Join(line, "\n"))
	}

	macros, _, err := macro.ExtractMacros(
		[]byte(text(
			`<!-- Macro: @\{([^}]+)\}`,
			`     Template: ac:link:user`,
			`     Name: ${1} -->`,

			// TODO(seletskiy): more macros here
		)),

		templates,
	)
	if err != nil {
		return nil, err
	}

	return macros, nil
}

func templates(api *confluence.API) (*template.Template, error) {
	text := func(line ...string) string {
		return strings.Join(line, ``)
	}

	templates := template.New(`stdlib`).Funcs(
		template.FuncMap{
			"user": func(name string) *confluence.User {
				user, err := api.GetUserByName(name)
				if err != nil {
					log.Error(err)
				}

				return user
			},

			// The only way to escape CDATA end marker ']]>' is to split it
			// into two CDATA sections.
			"cdata": func(data string) string {
				return strings.ReplaceAll(
					data,
					"]]>",
					"]]><![CDATA[]]]]><![CDATA[>",
				)
			},
		},
	)

	var err error

	for name, body := range map[string]string{
		// This template is used to select whole article layout
		`ac:layout`: text(
			`{{ if eq .Layout "article" }}`,
			/**/ `<ac:layout>`,
			/**/ `<ac:layout-section ac:type="two_right_sidebar">`,
			/**/ `<ac:layout-cell>{{ .Body }}</ac:layout-cell>`,
			/**/ `<ac:layout-cell></ac:layout-cell>`,
			/**/ `</ac:layout-section>`,
			/**/ `</ac:layout>`,
			`{{ else }}`,
			/**/ `{{ .Body }}`,
			`{{ end }}`,
		),

		// This template is used for rendering code in ```
		`ac:code`: text(
			`{{ if .Collapse }}<ac:structured-macro ac:name="expand">{{printf "\n"}}`,
			`{{ if .Title }}<ac:parameter ac:name="title">{{ .Title }}</ac:parameter>{{printf "\n"}}{{ end }}`,
			`<ac:rich-text-body>{{printf "\n"}}{{ end }}`,

			`<ac:structured-macro ac:name="code">{{printf "\n"}}`,
			/**/ `<ac:parameter ac:name="language">{{ .Language }}</ac:parameter>{{printf "\n"}}`,
			/**/ `<ac:parameter ac:name="collapse">{{ .Collapse }}</ac:parameter>{{printf "\n"}}`,
			/**/ `{{ if .Title }}<ac:parameter ac:name="title">{{ .Title }}</ac:parameter>{{printf "\n"}}{{ end }}`,
			/**/ `<ac:plain-text-body><![CDATA[{{ .Text | cdata }}]]></ac:plain-text-body>{{printf "\n"}}`,
			`</ac:structured-macro>{{printf "\n"}}`,

			`{{ if .Collapse }}</ac:rich-text-body>{{printf "\n"}}`,
			`</ac:structured-macro>{{printf "\n"}}{{ end }}`,
		),

		`ac:status`: text(
			`<ac:structured-macro ac:name="status">`,
			`<ac:parameter ac:name="colour">{{ or .Color "Grey" }}</ac:parameter>`,
			`<ac:parameter ac:name="title">{{ or .Title .Color }}</ac:parameter>`,
			`<ac:parameter ac:name="subtle">{{ or .Subtle false }}</ac:parameter>`,
			`</ac:structured-macro>`,
		),

		`ac:link:user`: text(
			`{{ with .Name | user }}`,
			/**/ `<ac:link>`,
			/**/ `<ri:user ri:account-id="{{ .AccountID }}"/>`,
			/**/ `</ac:link>`,
			`{{ else }}`,
			/**/ `{{ .Name }}`,
			`{{ end }}`,
		),

		`ac:jira:ticket`: text(
			`<ac:structured-macro ac:name="jira">`,
			`<ac:parameter ac:name="key">{{ .Ticket }}</ac:parameter>`,
			`</ac:structured-macro>`,
		),

		/* https://confluence.atlassian.com/conf59/info-tip-note-and-warning-macros-792499127.html */

		`ac:box`: text(
			`<ac:structured-macro ac:name="{{ .Name }}">{{printf "\n"}}`,
			`<ac:parameter ac:name="icon">{{ or .Icon "false" }}</ac:parameter>{{printf "\n"}}`,
			`<ac:parameter ac:name="title">{{ or .Title "" }}</ac:parameter>{{printf "\n"}}`,
			`<ac:rich-text-body>{{printf "\n"}}`,
			`{{ .Body }}{{printf "\n"}}`,
			`</ac:rich-text-body>{{printf "\n"}}`,
			`</ac:structured-macro>{{printf "\n"}}`,
		),

		/* https://confluence.atlassian.com/conf59/table-of-contents-macro-792499210.html */

		`ac:toc`: text(
			`<ac:structured-macro ac:name="toc">{{printf "\n"}}`,
			`<ac:parameter ac:name="printable">{{ or .Printable "true" }}</ac:parameter>{{printf "\n"}}`,
			`<ac:parameter ac:name="style">{{ or .Style "disc" }}</ac:parameter>{{printf "\n"}}`,
			`<ac:parameter ac:name="maxLevel">{{ or .MaxLevel "7" }}</ac:parameter>{{printf "\n"}}`,
			`<ac:parameter ac:name="indent">{{ or .Indent "" }}</ac:parameter>{{printf "\n"}}`,
			`<ac:parameter ac:name="minLevel">{{ or .MinLevel "1" }}</ac:parameter>{{printf "\n"}}`,
			`<ac:parameter ac:name="exclude">{{ or .Exclude "" }}</ac:parameter>{{printf "\n"}}`,
			`<ac:parameter ac:name="type">{{ or .Type "list" }}</ac:parameter>{{printf "\n"}}`,
			`<ac:parameter ac:name="outline">{{ or .Outline "clear" }}</ac:parameter>{{printf "\n"}}`,
			`<ac:parameter ac:name="include">{{ or .Include "" }}</ac:parameter>{{printf "\n"}}`,
			`</ac:structured-macro>{{printf "\n"}}`,
		),

		/* https://confluence.atlassian.com/doc/confluence-storage-format-790796544.html */

		`ac:emoticon`: text(
			`<ac:emoticon ac:name="{{ .Name }}"/>`,
		),

		// TODO(seletskiy): more templates here
	} {
		templates, err = templates.New(name).Parse(body)
		if err != nil {
			return nil, karma.
				Describe("template", body).
				Format(
					err,
					"unable to parse template",
				)
		}
	}

	return templates, nil
}
