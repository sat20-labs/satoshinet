package main

import (
	"encoding/json"
	"sort"

	"github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
)

type TopicCatalogSnapshot struct {
	Meta    TopicMeta
	State   TopicState
	Members []TopicMember
}

type TopicCatalogStore interface {
	SaveTopic(meta TopicMeta, state TopicState, members []TopicMember) error
	LoadTopics() ([]TopicCatalogSnapshot, error)
}

type DKVSTopicCatalogBackend interface {
	PutDKVSInternalTopicValues(values map[string][]byte) (int, error)
	ListDKVSInternalTopicRecords() ([]*wire.DKVSRecord, error)
}

type DKVSTopicCatalogStore struct {
	backend DKVSTopicCatalogBackend
}

func NewDKVSTopicCatalogStore(backend DKVSTopicCatalogBackend) *DKVSTopicCatalogStore {
	return &DKVSTopicCatalogStore{backend: backend}
}

func encodeTopicCatalogValue(value interface{}) ([]byte, error) {
	return json.Marshal(value)
}

func (s *DKVSTopicCatalogStore) SaveTopic(meta TopicMeta, state TopicState, members []TopicMember) error {
	if s == nil || s.backend == nil || meta.TopicName == "" || meta.OwnerAccount == "" || meta.ServiceCoreNode == "" {
		return ErrMessageInvalidEnvelope
	}
	metaKey, err := dkvs.TopicMetaKey(meta.TopicName)
	if err != nil {
		return err
	}
	stateKey, err := dkvs.TopicStateKey(meta.TopicName)
	if err != nil {
		return err
	}
	metaValue, err := encodeTopicCatalogValue(meta)
	if err != nil {
		return err
	}
	stateValue, err := encodeTopicCatalogValue(state)
	if err != nil {
		return err
	}
	values := map[string][]byte{metaKey: metaValue, stateKey: stateValue}
	for _, member := range members {
		if !validateAccountID(member.AccountID) {
			return ErrMessageInvalidEnvelope
		}
		key, err := dkvs.TopicMemberKey(meta.TopicName, member.AccountID)
		if err != nil {
			return err
		}
		value, err := encodeTopicCatalogValue(member)
		if err != nil {
			return err
		}
		values[key] = value
	}
	_, err = s.backend.PutDKVSInternalTopicValues(values)
	return err
}

func (s *DKVSTopicCatalogStore) LoadTopics() ([]TopicCatalogSnapshot, error) {
	if s == nil || s.backend == nil {
		return nil, ErrMessageInvalidEnvelope
	}
	records, err := s.backend.ListDKVSInternalTopicRecords()
	if err != nil {
		return nil, err
	}
	type buildingTopic struct {
		meta    *TopicMeta
		state   *TopicState
		members []TopicMember
	}
	building := make(map[string]*buildingTopic)
	for _, record := range records {
		if record == nil {
			continue
		}
		parsed, err := dkvs.ParseKey(record.Key)
		if err != nil || parsed.Namespace != "topic" || len(parsed.Segments) < 2 {
			return nil, ErrMessageInvalidEnvelope
		}
		topicName := parsed.Segments[0]
		item := building[topicName]
		if item == nil {
			item = &buildingTopic{}
			building[topicName] = item
		}
		switch parsed.Segments[1] {
		case "meta":
			var meta TopicMeta
			if err := json.Unmarshal(record.Value, &meta); err != nil || meta.TopicName != topicName {
				return nil, ErrMessageInvalidEnvelope
			}
			item.meta = &meta
		case "state":
			var state TopicState
			if err := json.Unmarshal(record.Value, &state); err != nil {
				return nil, ErrMessageInvalidEnvelope
			}
			item.state = &state
		case "members":
			if len(parsed.Segments) != 3 {
				return nil, ErrMessageInvalidEnvelope
			}
			var member TopicMember
			if err := json.Unmarshal(record.Value, &member); err != nil || member.AccountID != parsed.Segments[2] {
				return nil, ErrMessageInvalidEnvelope
			}
			item.members = append(item.members, member)
		default:
			return nil, ErrMessageInvalidEnvelope
		}
	}
	names := make([]string, 0, len(building))
	for name := range building {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]TopicCatalogSnapshot, 0, len(names))
	for _, name := range names {
		item := building[name]
		if item.meta == nil || item.state == nil {
			return nil, ErrMessageInvalidEnvelope
		}
		sort.Slice(item.members, func(i, j int) bool { return item.members[i].AccountID < item.members[j].AccountID })
		out = append(out, TopicCatalogSnapshot{Meta: *item.meta, State: *item.state, Members: item.members})
	}
	return out, nil
}
