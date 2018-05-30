/*
Copyright 2018 the Heptio Ark contributors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package restic

import (
	"fmt"
	"os/exec"
	"strings"
)

// Command represents a restic command.
type Command struct {
	BaseName     string
	Command      string
	RepoPrefix   string
	Repo         string
	PasswordFile string
	Args         []string
	ExtraFlags   []string
}

// StringSlice returns the command as a slice of strings.
func (c *Command) StringSlice() []string {
	var res []string
	if c.BaseName != "" {
		res = append(res, c.BaseName)
	} else {
		res = append(res, "/restic")
	}

	res = append(res, c.Command, repoFlag(c.RepoPrefix, c.Repo))
	if c.PasswordFile != "" {
		res = append(res, passwordFlag(c.PasswordFile))
	}
	res = append(res, c.Args...)
	res = append(res, c.ExtraFlags...)

	return res
}

// String returns the command as a string.
func (c *Command) String() string {
	return strings.Join(c.StringSlice(), " ")
}

// Cmd returns an exec.Cmd for the command.
func (c *Command) Cmd() *exec.Cmd {
	parts := c.StringSlice()
	return exec.Command(parts[0], parts[1:]...)
}

func repoFlag(prefix, repo string) string {
	return fmt.Sprintf("--repo=%s/%s", prefix, repo)
}

func passwordFlag(file string) string {
	return fmt.Sprintf("--password-file=%s", file)
}
