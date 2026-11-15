package main

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"

	"github.com/MetrolistGroup/metrobot/cmd"
	"github.com/MetrolistGroup/metrobot/internal/notefmt"
)

type request struct {
	Content string `json:"content"`
	Literal bool   `json:"literal"`
	Mode    string `json:"mode"`
}

type literal struct {
	Line    int    `json:"line"`
	Content string `json:"content"`
}

type response struct {
	Content  string           `json:"content"`
	Rendered notefmt.Rendered `json:"rendered"`
	Error    string           `json:"error,omitempty"`
	Literals []literal        `json:"literals,omitempty"`
}

func format(req request) (response, error) {
	if req.Mode == "base-prompt" {
		return response{Content: cmd.GarminSystemPrompt()}, nil
	}
	if req.Mode == "go" || req.Mode == "markdown" {
		result := response{Content: req.Content, Rendered: notefmt.Rendered{Text: req.Content}}
		if req.Mode == "go" {
			positions := token.NewFileSet()
			file, err := parser.ParseFile(positions, "draft.go", req.Content, parser.AllErrors)
			if err != nil {
				return response{}, err
			}
			ast.Inspect(file, func(node ast.Node) bool {
				if value, ok := node.(*ast.BasicLit); ok && value.Kind == token.STRING {
					text, err := strconv.Unquote(value.Value)
					if err == nil && text != "" {
						result.Literals = append(result.Literals, literal{positions.Position(value.Pos()).Line, text})
					}
				}
				return true
			})
		}
		return result, nil
	}
	content := notefmt.Normalize(req.Content)
	if strings.TrimSpace(content) == "" {
		return response{}, fmt.Errorf("note content is required")
	}
	note := &notefmt.Note{Parts: []notefmt.Part{{Text: content}}}
	if !req.Literal {
		var err error
		note, err = notefmt.Parse(content)
		if err != nil {
			return response{}, err
		}
		if err := note.Validate(); err != nil {
			return response{}, err
		}
	}
	return response{Content: content, Rendered: note.Render()}, nil
}

func main() {
	var req request
	var result response
	err := json.NewDecoder(os.Stdin).Decode(&req)
	if err == nil {
		result, err = format(req)
	}
	if err != nil {
		result.Error = err.Error()
	}
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
