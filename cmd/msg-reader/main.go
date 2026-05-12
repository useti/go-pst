// go-pst is a library for reading Personal Storage Table (.pst) files (written in Go/Golang).
//
// Copyright 2023 Marten Mooij
// Copyright 2025 Yury Tikhoglaz
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	pst "github.com/useti/go-pst/v6/pkg"
	"github.com/useti/go-pst/v6/pkg/properties"
	"golang.org/x/text/encoding"

	charsets "github.com/emersion/go-message/charset"
)

func main() {
	pst.ExtendCharsets(func(name string, enc encoding.Encoding) {
		charsets.RegisterEncoding(name, enc)
	})

	startTime := time.Now()

	fmt.Println("Initializing...")

	// Read all .msg files in ../data/msg
	msgDir := "../../data/msg"
	files, err := os.ReadDir(msgDir)
	if err != nil {
		panic(fmt.Sprintf("Failed to read msg directory: %+v\n", err))
	}

	for _, file := range files {
		if filepath.Ext(file.Name()) == ".msg" {
			fmt.Printf("Processing %s...\n", file.Name())
			processMSG(filepath.Join(msgDir, file.Name()))
		}
	}

	fmt.Printf("Time: %s\n", time.Since(startTime).String())
}

func processMSG(filePath string) {
	reader, err := os.Open(filePath)
	if err != nil {
		fmt.Printf("Failed to open MSG file %s: %+v\n", filePath, err)
		return
	}
	defer reader.Close()

	pstFile, err := pst.New(reader)
	if err != nil {
		fmt.Printf("Failed to parse MSG file %s: %+v\n", filePath, err)
		return
	}
	defer pstFile.Cleanup()

	if pstFile.ContentType != pst.ContentTypeMSG {
		fmt.Printf("File %s is not an MSG file\n", filePath)
		return
	}

	message := pstFile.GetRootMessage()
	if message == nil {
		fmt.Printf("No root message in %s\n", filePath)
		return
	}

	// Print message properties
	if msgProps, ok := message.Properties.(*properties.Message); ok {
		fmt.Printf("Subject: %s\n", msgProps.GetSubject())
		fmt.Printf("From: %s\n", msgProps.GetSenderName())
		fmt.Printf("To: %s\n", msgProps.GetReceivedByName())
		fmt.Printf("Body: %s\n", msgProps.GetBody())

		// Print attachments
		if message.Attachments != nil && len(message.Attachments) > 0 {
			fmt.Printf("Attachments (%d):\n", len(message.Attachments))
			for i, att := range message.Attachments {
				filename := "unknown"
				if att.AttachFilename != nil {
					filename = *att.AttachFilename
				} else if att.AttachLongFilename != nil {
					filename = *att.AttachLongFilename
				}
				fmt.Printf("  [%d] %s\n", i+1, filename)
			}
		}

		fmt.Println("---")
	} else {
		fmt.Printf("Message properties not of type Message\n")
		// printout type of properties
		fmt.Printf("Properties type: %T\n", message.Properties)
	}
}
