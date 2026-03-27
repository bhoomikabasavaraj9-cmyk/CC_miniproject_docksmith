package parser

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// InstructionType represents the type of a Docksmithfile instruction
type InstructionType string

const (
	FROM    InstructionType = "FROM"
	COPY    InstructionType = "COPY"
	RUN     InstructionType = "RUN"
	WORKDIR InstructionType = "WORKDIR"
	ENV     InstructionType = "ENV"
	CMD     InstructionType = "CMD"
)

// Instruction holds a single parsed instruction from the Docksmithfile
type Instruction struct {
	Type    InstructionType
	Args    string // raw argument string after the instruction keyword
	LineNum int
}

// ParseFile reads the Docksmithfile at the given path and returns a slice of Instructions.
// Returns an error if any unrecognised instruction is encountered (with line number).
func ParseFile(path string) ([]Instruction, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("could not open Docksmithfile: %w", err)
	}
	defer f.Close()

	var instructions []Instruction
	scanner := bufio.NewScanner(f)
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())

		// skip blank lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// split into keyword + rest
		parts := strings.SplitN(line, " ", 2)
		keyword := strings.ToUpper(parts[0])
		args := ""
		if len(parts) == 2 {
			args = strings.TrimSpace(parts[1])
		}

		switch InstructionType(keyword) {
		case FROM, COPY, RUN, WORKDIR, ENV, CMD:
			instructions = append(instructions, Instruction{
				Type:    InstructionType(keyword),
				Args:    args,
				LineNum: lineNum,
			})
		default:
			return nil, fmt.Errorf("unknown instruction %q on line %d", keyword, lineNum)
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading Docksmithfile: %w", err)
	}

	return instructions, nil
}
