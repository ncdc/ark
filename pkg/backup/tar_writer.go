/*
Copyright 2017 the Heptio Ark contributors.

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

package backup

import (
	"archive/tar"
	"io"
	"time"

	"github.com/pkg/errors"
)

type tarWriter interface {
	io.Closer
	Write([]byte) (int, error)
	WriteHeader(*tar.Header) error
}

type tarWriterItemStorage struct {
	w       tarWriter
	modTime time.Time
}

func (t *tarWriterItemStorage) Write(path string, item []byte) error {
	hdr := &tar.Header{
		Name:     path,
		Size:     int64(len(item)),
		Typeflag: tar.TypeReg,
		Mode:     0755,
		ModTime:  t.modTime,
	}

	if err := t.w.WriteHeader(hdr); err != nil {
		return errors.WithStack(err)
	}

	if _, err := t.w.Write(item); err != nil {
		return errors.WithStack(err)
	}

	return nil
}
