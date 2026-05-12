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

package pst

import (
	_ "embed"
	"fmt"
	"strings"

	"github.com/pkg/errors"
	"github.com/rotisserie/eris"
	"github.com/tinylib/msgp/msgp"
	"github.com/useti/go-pst/v6/pkg/properties"
)

// Message represents a message.
type Message struct {
	File                   *File
	Identifier             Identifier
	PropertyContext        *PropertyContext
	AttachmentTableContext *TableContext
	LocalDescriptors       []LocalDescriptor // Used by the PropertyContext and TableContext.
	Properties             msgp.Decodable    // Type properties.Message, properties.Appointment, properties.Contact
	Attachments            []*Attachment     // For MSG files, contains parsed attachments
}

// GetMessageTableContext returns the message table context of this folder which contains references to all messages.
// Note this only returns the identifier of each message.
func (folder *Folder) GetMessageTableContext() (TableContext, error) {
	emailsIdentifier := folder.Identifier + 12

	emailsNode, err := folder.File.GetNodeBTreeNode(emailsIdentifier)

	if err != nil {
		return TableContext{}, eris.Wrap(err, "failed to find node b-tree node")
	}

	localDescriptors, err := folder.File.GetLocalDescriptors(emailsNode)

	if err != nil {
		return TableContext{}, eris.Wrap(err, "failed to find local descriptors")
	}

	emailsDataNode, err := folder.File.GetDataBTreeNode(emailsIdentifier)

	if err != nil {
		return TableContext{}, eris.Wrap(err, "failed to find data b-tree node")
	}

	emailsHeapOnNode, err := folder.File.GetHeapOnNode(emailsDataNode)

	if err != nil {
		return TableContext{}, eris.Wrap(err, "failed to get Heap-on-Node")
	}

	// 26610 is a message property HNID.
	tableContext, err := folder.File.GetTableContext(emailsHeapOnNode, localDescriptors, 26610)

	if err != nil {
		return TableContext{}, eris.Wrap(err, "failed to get table context")
	}

	return tableContext, nil
}

// MessageIterator implements a message iterator.
type MessageIterator struct {
	file                *File
	messageTableContext TableContext

	err            error
	currentIndex   int
	currentMessage *Message
}

// Err return the error cause.
func (messageIterator *MessageIterator) Err() error {
	return messageIterator.err
}

// Next will ensure that Value returns the next item when executed.
// If the next value is not retrievable, Next will return false and Err() will return the error cause.
func (messageIterator *MessageIterator) Next() bool {
	hasNext := len(messageIterator.messageTableContext.Properties) > messageIterator.currentIndex

	if !hasNext {
		return false
	}

	var currentMessage *Message

	for _, property := range messageIterator.messageTableContext.Properties[messageIterator.currentIndex] {
		// We only return the message identifier in GetMessageTableContext,
		// so we don't need to check the property ID here.
		propertyReader, err := messageIterator.messageTableContext.GetPropertyReader(property)

		if err != nil {
			messageIterator.err = eris.Wrap(err, "failed to get property reader")
			return false
		}

		messageIdentifier, err := propertyReader.GetInteger32()

		if err != nil {
			messageIterator.err = eris.Wrap(err, "failed to get message identifier")
			return false
		}

		message, err := messageIterator.file.GetMessage(Identifier(messageIdentifier))

		if err != nil {
			messageIterator.err = eris.Wrapf(err, "failed to find message: %d", messageIdentifier)
			return false
		}

		currentMessage = message
	}

	messageIterator.currentIndex++
	messageIterator.currentMessage = currentMessage

	return true
}

// Value returns the current value in the iterator.
func (messageIterator *MessageIterator) Value() *Message {
	return messageIterator.currentMessage
}

// Size returns the amount of messages in the message iterator.
func (messageIterator *MessageIterator) Size() int {
	return len(messageIterator.messageTableContext.Properties)
}

func (messageIterator *MessageIterator) CurrentIndex() int {
	return messageIterator.currentIndex
}

// GetMessageIterator returns an iterator for messages.
func (folder *Folder) GetMessageIterator() (MessageIterator, error) {
	if folder.MessageCount == 0 {
		return MessageIterator{}, ErrMessagesNotFound
	} else if folder.Identifier.GetType() == IdentifierTypeSearchFolder {
		return MessageIterator{}, ErrMessagesNotFound
	}

	messageTableContext, err := folder.GetMessageTableContext()

	if err != nil {
		return MessageIterator{}, eris.Wrap(err, "failed to get message table context")
	}

	return MessageIterator{
		file:                folder.File,
		messageTableContext: messageTableContext,
	}, nil
}

// GetAllMessages returns an array of all messages from the message table context.
// See GetMessageIterator.
func (folder *Folder) GetAllMessages() ([]*Message, error) {
	messageIterator, err := folder.GetMessageIterator()

	if err != nil {
		return nil, err
	}

	var messages []*Message

	for messageIterator.Next() {
		messages = append(messages, messageIterator.Value())
	}

	return messages, messageIterator.Err()
}

// GetMessage returns the message of the identifier.
func (file *File) GetMessage(identifier Identifier) (*Message, error) {
	if identifier.GetType() != IdentifierTypeNormalMessage {
		return nil, ErrMessageIdentifierTypeInvalid
	}

	messageNode, err := file.GetNodeBTreeNode(identifier)

	if err != nil {
		return nil, eris.Wrap(err, "failed to find node b-tree node")
	}

	messageDataNode, err := file.GetBlockBTreeNode(messageNode.DataIdentifier)

	if err != nil {
		return nil, eris.Wrap(err, "failed to find block b-tree node")
	}

	messageHeapOnNode, err := file.GetHeapOnNode(messageDataNode)

	if err != nil {
		return nil, eris.Wrap(err, "failed to get Heap-on-Node")
	}

	localDescriptors, err := file.GetLocalDescriptors(messageNode)

	if err != nil {
		return nil, eris.Wrap(err, "failed to find local descriptors")
	}

	propertyContext, err := file.GetPropertyContextFromNode(messageHeapOnNode, messageNode)

	if err != nil {
		return nil, eris.Wrap(err, "failed to get property context")
	}

	var messageProperties msgp.Decodable

	messageClassPropertyReader, err := propertyContext.GetPropertyReader(26, localDescriptors)

	if err != nil {
		fmt.Printf("Failed to get message class property reader, falling back to properties.Message: %+v\n", eris.New(err.Error()))
		messageProperties = &properties.Message{}
	} else {
		messageClass, err := messageClassPropertyReader.GetString()

		if err != nil {
			fmt.Printf("Failed to get message class, falling back to properties.Message: %+v\n", eris.New(err.Error()))
			messageProperties = &properties.Message{}
		} else {
			// https://learn.microsoft.com/en-us/office/vba/outlook/concepts/forms/item-types-and-message-classes
			if messageClass == "IPM.Note" || messageClass == "IPM.Note.SMIME.MultipartSigned" {
				messageProperties = &properties.Message{}
			} else if messageClass == "IPM.Appointment" || messageClass == "IPM.Schedule.Meeting" || messageClass == "IPM.Schedule.Meeting.Request" || messageClass == "IPM.OLE.CLASS.{00061055-0000-0000-C000-000000000046}" {
				messageProperties = &properties.Appointment{}
			} else if messageClass == "IPM.Contact" || messageClass == "IPM.AbchPerson" {
				messageProperties = &properties.Contact{}
			} else if messageClass == "IPM.Task" {
				messageProperties = &properties.Task{}
			} else if messageClass == "IPM.Activity" {
				messageProperties = &properties.Journal{}
			} else if messageClass == "IPM.Post.Rss" {
				messageProperties = &properties.RSS{}
			} else if messageClass == "IPM.DistList" {
				messageProperties = &properties.AddressBook{}
			} else {
				fmt.Printf("Unmapped message class \"%s\", falling back to properties.Message...\n", messageClass)
				messageProperties = &properties.Message{}
			}
		}
	}

	if err := propertyContext.Populate(messageProperties, localDescriptors); err != nil {
		return nil, eris.Wrap(err, "failed to populate message properties")
	}

	return &Message{
		File:             file,
		Identifier:       identifier,
		PropertyContext:  propertyContext,
		LocalDescriptors: localDescriptors,
		Properties:       messageProperties,
	}, nil
}

// GetBodyRTF return the RTF body, may be
func (message *Message) GetBodyRTF() (string, error) {
	rtfPropertyReader, err := message.PropertyContext.GetPropertyReader(4105, message.LocalDescriptors)

	if err != nil {
		return "", err
	}

	rtfBody := make([]byte, rtfPropertyReader.Size())

	if _, err := rtfPropertyReader.ReadAt(rtfBody, 0); err != nil {
		return "", errors.WithStack(err)
	}

	return NewRTFDecoder().Decode(rtfBody)
}

// OrphanMessageIterator iterates over orphan (deleted/unreferenced) messages.
// This is only available in recovery mode.
type OrphanMessageIterator struct {
	file           *File
	messageNodes   []BTreeNode
	referencedMsgs map[Identifier]bool
	err            error
	currentIndex   int
	currentMessage *Message
	// Statistics for debugging
	TotalNodes        int
	SkippedReferenced int
	SkippedErrors     int
	ErrorMessages     []string
}

// Err returns the error cause.
func (iter *OrphanMessageIterator) Err() error {
	return iter.err
}

// Next advances to the next orphan message.
// Returns false when there are no more messages or an error occurred.
func (iter *OrphanMessageIterator) Next() bool {
	for iter.currentIndex < len(iter.messageNodes) {
		node := iter.messageNodes[iter.currentIndex]
		iter.currentIndex++

		// Skip messages that are referenced by folders (not orphans)
		if iter.referencedMsgs[node.Identifier] {
			iter.SkippedReferenced++
			continue
		}

		// Try to get the message - may fail for corrupted messages
		message, err := iter.file.getMessageRecovery(node.Identifier)
		if err != nil {
			// Track errors for debugging
			iter.SkippedErrors++
			iter.ErrorMessages = append(iter.ErrorMessages, fmt.Sprintf("ID %d: %v", node.Identifier, err))
			continue
		}

		iter.currentMessage = message
		return true
	}
	return false
}

// Value returns the current orphan message.
func (iter *OrphanMessageIterator) Value() *Message {
	return iter.currentMessage
}

// Size returns the total number of message nodes (including referenced ones).
func (iter *OrphanMessageIterator) Size() int {
	return len(iter.messageNodes)
}

// GetOrphanMessageIterator returns an iterator for orphan (deleted/unreferenced) messages.
// This requires RecoveryMode to be enabled.
// Orphan messages are messages that exist in the Node B-tree but are not
// referenced by any folder in the PST file hierarchy.
func (file *File) GetOrphanMessageIterator() (*OrphanMessageIterator, error) {
	if !file.RecoveryMode {
		return nil, eris.New("go-pst: orphan message iteration requires RecoveryMode to be enabled")
	}

	// Collect all message nodes from the Node B-tree
	var messageNodes []BTreeNode
	totalNodes := 0
	file.NodeBTree.Scan(func(node BTreeNode) bool {
		totalNodes++
		// Only include leaf nodes (NodeLevel == 0) with message identifier type
		if node.NodeLevel == 0 && node.Identifier.GetType() == IdentifierTypeNormalMessage {
			messageNodes = append(messageNodes, node)
		}
		return true // continue scanning
	})

	// Build set of referenced message identifiers
	referencedMsgs := make(map[Identifier]bool)
	file.collectReferencedMessages(referencedMsgs)

	return &OrphanMessageIterator{
		file:           file,
		messageNodes:   messageNodes,
		referencedMsgs: referencedMsgs,
		TotalNodes:     totalNodes,
	}, nil
}

// GetRecoverableMessagesIterator returns an iterator that attempts to recover messages
// from ALL nodes in the B-tree, regardless of their identifier type.
// This is useful for deeply corrupted files where identifier types may be corrupted.
// This requires RecoveryMode to be enabled.
func (file *File) GetRecoverableMessagesIterator() (*RecoverableMessagesIterator, error) {
	if !file.RecoveryMode {
		return nil, eris.New("go-pst: recoverable messages iteration requires RecoveryMode to be enabled")
	}

	// Collect ALL leaf nodes from the Node B-tree
	var allNodes []BTreeNode
	file.NodeBTree.Scan(func(node BTreeNode) bool {
		if node.NodeLevel == 0 { // Only leaf nodes
			allNodes = append(allNodes, node)
		}
		return true
	})

	return &RecoverableMessagesIterator{
		file:     file,
		allNodes: allNodes,
	}, nil
}

// RecoveredItemType represents the type of item recovered.
type RecoveredItemType string

const (
	RecoveredItemTypeMessage     RecoveredItemType = "Message"
	RecoveredItemTypeAppointment RecoveredItemType = "Appointment"
	RecoveredItemTypeContact     RecoveredItemType = "Contact"
	RecoveredItemTypeTask        RecoveredItemType = "Task"
	RecoveredItemTypeJournal     RecoveredItemType = "Journal"
	RecoveredItemTypeNote        RecoveredItemType = "Note"
	RecoveredItemTypeRSS         RecoveredItemType = "RSS"
	RecoveredItemTypeAddressBook RecoveredItemType = "AddressBook"
	RecoveredItemTypeAttachment  RecoveredItemType = "Attachment"
	RecoveredItemTypeFolder      RecoveredItemType = "Folder"
	RecoveredItemTypeUnknown     RecoveredItemType = "Unknown"
)

// RecoveredItem represents an item recovered from a corrupted PST file.
// It provides a unified interface for accessing properties from any recovered item type.
type RecoveredItem struct {
	// Identifier is the node identifier.
	Identifier Identifier
	// ItemType is the type of item (Message, Contact, Appointment, etc.)
	ItemType RecoveredItemType
	// PropertyContext provides raw property access.
	PropertyContext *PropertyContext
	// LocalDescriptors for accessing external data.
	LocalDescriptors []LocalDescriptor
	// Properties contains the parsed properties (may be nil if parsing failed).
	// Type depends on ItemType: *properties.Message, *properties.Contact, etc.
	Properties interface{}
	// NodeType is the original node identifier type.
	NodeType IdentifierType
}

// GetSubject returns the subject (PidTagSubject, ID 55) if available.
func (item *RecoveredItem) GetSubject() string {
	if item.PropertyContext == nil {
		return ""
	}
	if reader, err := item.PropertyContext.GetPropertyReader(55, item.LocalDescriptors); err == nil {
		if subj, err := reader.GetString(); err == nil {
			return subj
		}
	}
	return ""
}

// GetDisplayName returns the display name (PidTagDisplayName, ID 12289) if available.
func (item *RecoveredItem) GetDisplayName() string {
	if item.PropertyContext == nil {
		return ""
	}
	if reader, err := item.PropertyContext.GetPropertyReader(12289, item.LocalDescriptors); err == nil {
		if name, err := reader.GetString(); err == nil {
			return name
		}
	}
	return ""
}

// GetMessageClass returns the message class (PidTagMessageClass, ID 26) if available.
func (item *RecoveredItem) GetMessageClass() string {
	if item.PropertyContext == nil {
		return ""
	}
	if reader, err := item.PropertyContext.GetPropertyReader(26, item.LocalDescriptors); err == nil {
		if class, err := reader.GetString(); err == nil {
			return class
		}
	}
	return ""
}

// GetBody returns the plain text body (PidTagBody, ID 4096) if available.
func (item *RecoveredItem) GetBody() string {
	if item.PropertyContext == nil {
		return ""
	}
	if reader, err := item.PropertyContext.GetPropertyReader(4096, item.LocalDescriptors); err == nil {
		if body, err := reader.GetString(); err == nil {
			return body
		}
	}
	return ""
}

// GetSenderName returns the sender name (PidTagSenderName, ID 3098) if available.
func (item *RecoveredItem) GetSenderName() string {
	if item.PropertyContext == nil {
		return ""
	}
	if reader, err := item.PropertyContext.GetPropertyReader(3098, item.LocalDescriptors); err == nil {
		if name, err := reader.GetString(); err == nil {
			return name
		}
	}
	return ""
}

// GetSenderEmailAddress returns the sender email (PidTagSenderEmailAddress, ID 3103) if available.
func (item *RecoveredItem) GetSenderEmailAddress() string {
	if item.PropertyContext == nil {
		return ""
	}
	if reader, err := item.PropertyContext.GetPropertyReader(3103, item.LocalDescriptors); err == nil {
		if email, err := reader.GetString(); err == nil {
			return email
		}
	}
	return ""
}

// GetDisplayInfo returns the best available display info (subject or display name).
func (item *RecoveredItem) GetDisplayInfo() string {
	if subj := item.GetSubject(); subj != "" {
		return subj
	}
	return item.GetDisplayName()
}

// RecoverableMessagesIterator attempts to recover messages from all nodes.
type RecoverableMessagesIterator struct {
	file           *File
	allNodes       []BTreeNode
	currentIndex   int
	currentMessage *Message
	currentNode    BTreeNode
	currentType    RecoveredItemType
	// Statistics
	TriedNodes    int
	SuccessNodes  int
	FailedNodes   int
	ErrorMessages []string
	// Counts by type
	TypeCounts map[RecoveredItemType]int
	// Error categories
	ErrorCategoryCounts map[string]int
}

// Next advances to the next recoverable message.
func (iter *RecoverableMessagesIterator) Next() bool {
	if iter.TypeCounts == nil {
		iter.TypeCounts = make(map[RecoveredItemType]int)
	}
	if iter.ErrorCategoryCounts == nil {
		iter.ErrorCategoryCounts = make(map[string]int)
	}

	for iter.currentIndex < len(iter.allNodes) {
		node := iter.allNodes[iter.currentIndex]
		iter.currentIndex++
		iter.TriedNodes++

		// Try to parse this node as a message/item
		message, itemType, err := iter.file.tryRecoverItemFromNode(node)
		if err != nil {
			iter.FailedNodes++

			// Categorize errors
			errStr := err.Error()
			category := "other"
			if strings.Contains(errStr, "external node, no local descriptors") {
				category = "external_node"
			} else if strings.Contains(errStr, "failed to find b-tree node") || strings.Contains(errStr, "failed to find block") {
				category = "missing_block"
			} else if strings.Contains(errStr, "unexpected EOF") {
				category = "truncated_data"
			} else if strings.Contains(errStr, "HID type cannot be recovered") {
				category = "hid_type"
			}
			iter.ErrorCategoryCounts[category]++

			if len(iter.ErrorMessages) < 20 { // Keep first 20 errors
				iter.ErrorMessages = append(iter.ErrorMessages, fmt.Sprintf("ID %d (type %d): %v", node.Identifier, node.Identifier.GetType(), err))
			}
			continue
		}

		iter.SuccessNodes++
		iter.TypeCounts[itemType]++
		iter.currentMessage = message
		iter.currentNode = node
		iter.currentType = itemType
		return true
	}
	return false
}

// Value returns the current recovered message.
func (iter *RecoverableMessagesIterator) Value() *Message {
	return iter.currentMessage
}

// CurrentNodeType returns the identifier type of the current node.
func (iter *RecoverableMessagesIterator) CurrentNodeType() IdentifierType {
	return iter.currentNode.Identifier.GetType()
}

// CurrentItemType returns the type of the current recovered item.
func (iter *RecoverableMessagesIterator) CurrentItemType() RecoveredItemType {
	return iter.currentType
}

// Size returns the total number of nodes to try.
func (iter *RecoverableMessagesIterator) Size() int {
	return len(iter.allNodes)
}

// RecoveredItem returns the current item as a RecoveredItem with helper methods.
func (iter *RecoverableMessagesIterator) RecoveredItem() *RecoveredItem {
	if iter.currentMessage == nil {
		return nil
	}
	return &RecoveredItem{
		Identifier:       iter.currentMessage.Identifier,
		ItemType:         iter.currentType,
		PropertyContext:  iter.currentMessage.PropertyContext,
		LocalDescriptors: iter.currentMessage.LocalDescriptors,
		Properties:       iter.currentMessage.Properties,
		NodeType:         iter.currentNode.Identifier.GetType(),
	}
}

// getItemTypeFromMessageClass determines the item type from message class.
func getItemTypeFromMessageClass(messageClass string) RecoveredItemType {
	switch {
	case strings.HasPrefix(messageClass, "IPM.Note"):
		return RecoveredItemTypeMessage
	case strings.HasPrefix(messageClass, "IPM.Appointment") ||
		strings.HasPrefix(messageClass, "IPM.Schedule.Meeting") ||
		strings.Contains(messageClass, "00061055-0000-0000-C000-000000000046"):
		return RecoveredItemTypeAppointment
	case strings.HasPrefix(messageClass, "IPM.Contact") ||
		strings.HasPrefix(messageClass, "IPM.AbchPerson"):
		return RecoveredItemTypeContact
	case strings.HasPrefix(messageClass, "IPM.Task"):
		return RecoveredItemTypeTask
	case strings.HasPrefix(messageClass, "IPM.Activity"):
		return RecoveredItemTypeJournal
	case strings.HasPrefix(messageClass, "IPM.Post.Rss"):
		return RecoveredItemTypeRSS
	case strings.HasPrefix(messageClass, "IPM.DistList"):
		return RecoveredItemTypeAddressBook
	case strings.HasPrefix(messageClass, "IPM.StickyNote"):
		return RecoveredItemTypeNote
	default:
		return RecoveredItemTypeMessage
	}
}

// tryRecoverItemFromNode attempts to recover any item from a node.
// Returns the message, item type, and error.
func (file *File) tryRecoverItemFromNode(node BTreeNode) (*Message, RecoveredItemType, error) {
	nodeType := node.Identifier.GetType()

	// Handle different node types
	switch nodeType {
	case IdentifierTypeHID:
		// HID (Heap ID) - not a standalone item
		return nil, RecoveredItemTypeUnknown, fmt.Errorf("HID type cannot be recovered as item")

	case IdentifierTypeInternal:
		// Internal nodes - try to parse as property context anyway
		return file.tryRecoverPropertyContext(node, RecoveredItemTypeUnknown)

	case IdentifierTypeNormalFolder, IdentifierTypeSearchFolder:
		// Folder - extract folder information
		return file.tryRecoverPropertyContext(node, RecoveredItemTypeFolder)

	case IdentifierTypeNormalMessage, IdentifierTypeAssociatedMessage:
		// Messages - this is the normal case
		return file.tryRecoverMessage(node)

	case IdentifierTypeAttachment:
		// Attachment - has its own structure
		return file.tryRecoverPropertyContext(node, RecoveredItemTypeAttachment)

	case IdentifierTypeContentsTableIndex,
		IdentifierTypeReceiveFolderTable,
		IdentifierTypeOutgoingQueueTable,
		IdentifierTypeHierarchyTable,
		IdentifierTypeContentsTable,
		IdentifierTypeAssociatedContentsTable,
		IdentifierTypeSearchContentsTable,
		IdentifierTypeAttachmentTable,
		IdentifierTypeRecipientTable,
		IdentifierTypeSearchTableIndex,
		IdentifierTypeLTP:
		// These are tables, not items - try to extract anyway in aggressive mode
		return file.tryRecoverPropertyContext(node, RecoveredItemTypeUnknown)

	default:
		// Unknown type - try to parse anyway
		return file.tryRecoverMessage(node)
	}
}

// tryRecoverPropertyContext tries to recover a property context from a node.
func (file *File) tryRecoverPropertyContext(node BTreeNode, itemType RecoveredItemType) (*Message, RecoveredItemType, error) {
	messageDataNode, err := file.GetBlockBTreeNode(node.DataIdentifier)
	if err != nil {
		return nil, RecoveredItemTypeUnknown, eris.Wrap(err, "failed to find block b-tree node")
	}

	messageHeapOnNode, err := file.GetHeapOnNode(messageDataNode)
	if err != nil {
		return nil, RecoveredItemTypeUnknown, eris.Wrap(err, "failed to get Heap-on-Node")
	}

	localDescriptors, _ := file.GetLocalDescriptors(node) // Ignore error

	// Use origin-aware GetPropertyContextWithLocalDescriptors to handle external nodes and neighbor inference
	propertyContext, err := file.GetPropertyContextWithLocalDescriptorsFromNode(messageHeapOnNode, node, localDescriptors)
	if err != nil {
		// Fallback to origin-aware GetPropertyContextFromNode
		propertyContext, err = file.GetPropertyContextFromNode(messageHeapOnNode, node)
		if err != nil {
			return nil, RecoveredItemTypeUnknown, eris.Wrap(err, "failed to get property context")
		}
	}

	// Try to determine item type from message class if not already known
	actualType := itemType
	if itemType == RecoveredItemTypeUnknown {
		messageClassPropertyReader, err := propertyContext.GetPropertyReader(26, localDescriptors)
		if err == nil {
			messageClass, err := messageClassPropertyReader.GetString()
			if err == nil {
				actualType = getItemTypeFromMessageClass(messageClass)
			}
		}
	}

	// Create generic message container
	messageProperties := &properties.Message{}
	_ = propertyContext.Populate(messageProperties, localDescriptors)

	return &Message{
		File:             file,
		Identifier:       node.Identifier,
		PropertyContext:  propertyContext,
		LocalDescriptors: localDescriptors,
		Properties:       messageProperties,
	}, actualType, nil
}

// tryRecoverMessage attempts to recover a message from a node.
func (file *File) tryRecoverMessage(node BTreeNode) (*Message, RecoveredItemType, error) {
	messageDataNode, err := file.GetBlockBTreeNode(node.DataIdentifier)
	if err != nil {
		return nil, RecoveredItemTypeUnknown, eris.Wrap(err, "failed to find block b-tree node")
	}

	messageHeapOnNode, err := file.GetHeapOnNode(messageDataNode)
	if err != nil {
		return nil, RecoveredItemTypeUnknown, eris.Wrap(err, "failed to get Heap-on-Node")
	}

	localDescriptors, _ := file.GetLocalDescriptors(node) // Ignore error

	// Use origin-aware GetPropertyContextWithLocalDescriptors to handle external nodes and neighbor inference
	propertyContext, err := file.GetPropertyContextWithLocalDescriptorsFromNode(messageHeapOnNode, node, localDescriptors)
	if err != nil {
		// Fallback to origin-aware GetPropertyContextFromNode
		propertyContext, err = file.GetPropertyContextFromNode(messageHeapOnNode, node)
		if err != nil {
			return nil, RecoveredItemTypeUnknown, eris.Wrap(err, "failed to get property context")
		}
	}

	// Try to get message class
	var messageProperties msgp.Decodable = &properties.Message{}
	itemType := RecoveredItemTypeMessage

	messageClassPropertyReader, err := propertyContext.GetPropertyReader(26, localDescriptors)
	if err == nil {
		messageClass, err := messageClassPropertyReader.GetString()
		if err == nil {
			itemType = getItemTypeFromMessageClass(messageClass)
			// Map message class to properties type
			switch {
			case strings.HasPrefix(messageClass, "IPM.Note"):
				messageProperties = &properties.Message{}
			case strings.HasPrefix(messageClass, "IPM.Appointment") ||
				strings.HasPrefix(messageClass, "IPM.Schedule.Meeting") ||
				strings.Contains(messageClass, "00061055-0000-0000-C000-000000000046"):
				messageProperties = &properties.Appointment{}
			case strings.HasPrefix(messageClass, "IPM.Contact") ||
				strings.HasPrefix(messageClass, "IPM.AbchPerson"):
				messageProperties = &properties.Contact{}
			case strings.HasPrefix(messageClass, "IPM.Task"):
				messageProperties = &properties.Task{}
			case strings.HasPrefix(messageClass, "IPM.Activity"):
				messageProperties = &properties.Journal{}
			case strings.HasPrefix(messageClass, "IPM.Post.Rss"):
				messageProperties = &properties.RSS{}
			case strings.HasPrefix(messageClass, "IPM.DistList"):
				messageProperties = &properties.AddressBook{}
			case strings.HasPrefix(messageClass, "IPM.StickyNote"):
				messageProperties = &properties.Note{}
			default:
				messageProperties = &properties.Message{}
			}
		}
	}

	// Try to populate
	_ = propertyContext.Populate(messageProperties, localDescriptors) // Ignore error

	return &Message{
		File:             file,
		Identifier:       node.Identifier,
		PropertyContext:  propertyContext,
		LocalDescriptors: localDescriptors,
		Properties:       messageProperties,
	}, itemType, nil
}

// GetRawPropertyContextIterator returns an iterator that attempts to read property contexts
// from ALL nodes, regardless of type. This is the most aggressive recovery mode.
// Useful for severely corrupted files where even node types are damaged.
func (file *File) GetRawPropertyContextIterator() (*RawPropertyContextIterator, error) {
	if !file.RecoveryMode {
		return nil, eris.New("go-pst: raw property context iteration requires RecoveryMode to be enabled")
	}

	var allNodes []BTreeNode
	file.NodeBTree.Scan(func(node BTreeNode) bool {
		if node.NodeLevel == 0 {
			allNodes = append(allNodes, node)
		}
		return true
	})

	return &RawPropertyContextIterator{
		file:     file,
		allNodes: allNodes,
	}, nil
}

// RawPropertyContextIterator attempts to read property contexts from all nodes.
type RawPropertyContextIterator struct {
	file                   *File
	allNodes               []BTreeNode
	currentIndex           int
	currentPropertyContext *PropertyContext
	currentNode            BTreeNode
	TriedNodes             int
	SuccessNodes           int
	FailedNodes            int
}

// Next advances to the next property context.
func (iter *RawPropertyContextIterator) Next() bool {
	for iter.currentIndex < len(iter.allNodes) {
		node := iter.allNodes[iter.currentIndex]
		iter.currentIndex++
		iter.TriedNodes++

		// Try to get property context from this node
		dataNode, err := iter.file.GetBlockBTreeNode(node.DataIdentifier)
		if err != nil {
			iter.FailedNodes++
			continue
		}

		heapOnNode, err := iter.file.GetHeapOnNode(dataNode)
		if err != nil {
			iter.FailedNodes++
			continue
		}

		// Get local descriptors for this node
		localDescriptors, _ := iter.file.GetLocalDescriptors(node)

		// Try with local descriptors first
		propertyContext, err := iter.file.GetPropertyContextWithLocalDescriptors(heapOnNode, localDescriptors)
		if err != nil {
			// Fallback to regular method
			propertyContext, err = iter.file.GetPropertyContext(heapOnNode)
			if err != nil {
				iter.FailedNodes++
				continue
			}
		}

		iter.SuccessNodes++
		iter.currentPropertyContext = propertyContext
		iter.currentNode = node
		return true
	}
	return false
}

// Value returns the current property context.
func (iter *RawPropertyContextIterator) Value() *PropertyContext {
	return iter.currentPropertyContext
}

// CurrentNode returns the current node.
func (iter *RawPropertyContextIterator) CurrentNode() BTreeNode {
	return iter.currentNode
}

// Size returns the total number of nodes.
func (iter *RawPropertyContextIterator) Size() int {
	return len(iter.allNodes)
}

// collectReferencedMessages collects all message identifiers that are referenced by folders.
func (file *File) collectReferencedMessages(referenced map[Identifier]bool) {
	// Walk all folders and collect their message identifiers
	_ = file.WalkFolders(func(folder *Folder) error {
		if folder.MessageCount == 0 {
			return nil
		}
		if folder.Identifier.GetType() == IdentifierTypeSearchFolder {
			return nil
		}

		messageTableContext, err := folder.GetMessageTableContext()
		if err != nil {
			// Skip folders that can't be read
			return nil
		}

		for _, row := range messageTableContext.Properties {
			for _, property := range row {
				propertyReader, err := messageTableContext.GetPropertyReader(property)
				if err != nil {
					continue
				}

				messageIdentifier, err := propertyReader.GetInteger32()
				if err != nil {
					continue
				}

				referenced[Identifier(messageIdentifier)] = true
			}
		}

		return nil
	})
}

// getMessageRecovery attempts to get a message in recovery mode, with relaxed validation.
func (file *File) getMessageRecovery(identifier Identifier) (*Message, error) {
	// In recovery mode, we skip the identifier type check
	messageNode, err := file.GetNodeBTreeNode(identifier)
	if err != nil {
		return nil, eris.Wrap(err, "failed to find node b-tree node")
	}

	messageDataNode, err := file.GetBlockBTreeNode(messageNode.DataIdentifier)
	if err != nil {
		return nil, eris.Wrap(err, "failed to find block b-tree node")
	}

	messageHeapOnNode, err := file.GetHeapOnNode(messageDataNode)
	if err != nil {
		return nil, eris.Wrap(err, "failed to get Heap-on-Node")
	}

	localDescriptors, err := file.GetLocalDescriptors(messageNode)
	if err != nil {
		// In recovery mode, continue without local descriptors
		localDescriptors = nil
	}

	propertyContext, err := file.GetPropertyContext(messageHeapOnNode)
	if err != nil {
		return nil, eris.Wrap(err, "failed to get property context")
	}

	var messageProperties msgp.Decodable

	messageClassPropertyReader, err := propertyContext.GetPropertyReader(26, localDescriptors)
	if err != nil {
		messageProperties = &properties.Message{}
	} else {
		messageClass, err := messageClassPropertyReader.GetString()
		if err != nil {
			messageProperties = &properties.Message{}
		} else {
			// Map message class to properties type
			switch {
			case messageClass == "IPM.Note" || messageClass == "IPM.Note.SMIME.MultipartSigned":
				messageProperties = &properties.Message{}
			case messageClass == "IPM.Appointment" || messageClass == "IPM.Schedule.Meeting" || messageClass == "IPM.Schedule.Meeting.Request" || messageClass == "IPM.OLE.CLASS.{00061055-0000-0000-C000-000000000046}":
				messageProperties = &properties.Appointment{}
			case messageClass == "IPM.Contact" || messageClass == "IPM.AbchPerson":
				messageProperties = &properties.Contact{}
			case messageClass == "IPM.Task":
				messageProperties = &properties.Task{}
			case messageClass == "IPM.Activity":
				messageProperties = &properties.Journal{}
			case messageClass == "IPM.Post.Rss":
				messageProperties = &properties.RSS{}
			case messageClass == "IPM.DistList":
				messageProperties = &properties.AddressBook{}
			default:
				messageProperties = &properties.Message{}
			}
		}
	}

	if err := propertyContext.Populate(messageProperties, localDescriptors); err != nil {
		// In recovery mode, return message even if populate fails
		return &Message{
			File:             file,
			Identifier:       identifier,
			PropertyContext:  propertyContext,
			LocalDescriptors: localDescriptors,
			Properties:       &properties.Message{},
		}, nil
	}

	return &Message{
		File:             file,
		Identifier:       identifier,
		PropertyContext:  propertyContext,
		LocalDescriptors: localDescriptors,
		Properties:       messageProperties,
	}, nil
}

// GetMessageFromLocalDescriptor attempts to extract a message from a recovered LocalDescriptor.
// This is used for extracting actual message data during recovery.
func (file *File) GetMessageFromLocalDescriptor(localDescriptor LocalDescriptor) (*Message, error) {
	heapOnNode, err := file.GetHeapOnNodeFromLocalDescriptor(localDescriptor)
	if err != nil {
		return nil, eris.Wrap(err, "failed to get Heap-on-Node from local descriptor")
	}

	propertyContext, err := file.GetPropertyContext(heapOnNode)
	if err != nil {
		return nil, eris.Wrap(err, "failed to get property context")
	}

	var messageProperties msgp.Decodable

	// Try to determine message type from class
	messageClassPropertyReader, err := propertyContext.GetPropertyReader(26, nil)
	if err != nil {
		messageProperties = &properties.Message{}
	} else {
		messageClass, err := messageClassPropertyReader.GetString()
		if err != nil {
			messageProperties = &properties.Message{}
		} else {
			switch {
			case messageClass == "IPM.Note" || messageClass == "IPM.Note.SMIME.MultipartSigned":
				messageProperties = &properties.Message{}
			case messageClass == "IPM.Appointment" || messageClass == "IPM.Schedule.Meeting":
				messageProperties = &properties.Appointment{}
			case messageClass == "IPM.Contact":
				messageProperties = &properties.Contact{}
			case messageClass == "IPM.Task":
				messageProperties = &properties.Task{}
			default:
				messageProperties = &properties.Message{}
			}
		}
	}

	if err := propertyContext.Populate(messageProperties, nil); err != nil {
		return nil, eris.Wrap(err, "failed to populate message properties")
	}

	return &Message{
		File:            file,
		Identifier:      localDescriptor.Identifier,
		PropertyContext: propertyContext,
		Properties:      messageProperties,
	}, nil
}

// GetAllOrphanMessages returns all orphan (deleted/unreferenced) messages.
// This requires RecoveryMode to be enabled.
func (file *File) GetAllOrphanMessages() ([]*Message, error) {
	iter, err := file.GetOrphanMessageIterator()
	if err != nil {
		return nil, err
	}

	var messages []*Message
	for iter.Next() {
		messages = append(messages, iter.Value())
	}

	return messages, iter.Err()
}

// RecoveryResult contains the results of a recovery scan.
type RecoveryResult struct {
	Items        []*RecoveredItem
	TypeCounts   map[RecoveredItemType]int
	TriedNodes   int
	SuccessNodes int
	FailedNodes  int
	Errors       []string
}

// GetAllRecoverableItems scans all nodes and returns all recoverable items.
// This is the most comprehensive recovery method, returning all item types.
// Requires RecoveryMode to be enabled.
func (file *File) GetAllRecoverableItems() (*RecoveryResult, error) {
	iter, err := file.GetRecoverableMessagesIterator()
	if err != nil {
		return nil, err
	}

	var items []*RecoveredItem
	for iter.Next() {
		items = append(items, iter.RecoveredItem())
	}

	return &RecoveryResult{
		Items:        items,
		TypeCounts:   iter.TypeCounts,
		TriedNodes:   iter.TriedNodes,
		SuccessNodes: iter.SuccessNodes,
		FailedNodes:  iter.FailedNodes,
		Errors:       iter.ErrorMessages,
	}, nil
}

// GetRecoverableItemsByType returns all recovered items of a specific type.
// Requires RecoveryMode to be enabled.
func (file *File) GetRecoverableItemsByType(itemType RecoveredItemType) ([]*RecoveredItem, error) {
	result, err := file.GetAllRecoverableItems()
	if err != nil {
		return nil, err
	}

	var filtered []*RecoveredItem
	for _, item := range result.Items {
		if item.ItemType == itemType {
			filtered = append(filtered, item)
		}
	}
	return filtered, nil
}
