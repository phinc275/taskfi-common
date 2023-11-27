package taskfi_common

import (
	"github.com/confluentinc/confluent-kafka-go/v2/kafka"
	"github.com/confluentinc/confluent-kafka-go/v2/schemaregistry"
	"github.com/confluentinc/confluent-kafka-go/v2/schemaregistry/serde"
	"github.com/confluentinc/confluent-kafka-go/v2/schemaregistry/serde/protobuf"
	"github.com/gogo/protobuf/proto"
)

type KafkaProducerConfig struct {
	BootstrapServers string
	ClientID         string
	Acks             string

	SchemaRegistryURL string
}

type KafkaProducer struct {
	config     *KafkaProducerConfig
	producer   *kafka.Producer
	serializer serde.Serializer

	onMessageDelivered      func(message kafka.Message)
	onMessageDeliveryFailed func(message kafka.Message)
}

func WithOnMessageDelivered(onMessageDelivered func(message kafka.Message)) func(producer *KafkaProducer) {
	return func(producer *KafkaProducer) {
		producer.onMessageDelivered = onMessageDelivered
	}
}

func WithOnMessageDeliveryFailed(onMessageDeliveryFailed func(message kafka.Message)) func(producer *KafkaProducer) {
	return func(producer *KafkaProducer) {
		producer.onMessageDeliveryFailed = onMessageDeliveryFailed
	}
}

func NewKafkaProducer(cfg *KafkaProducerConfig, opts ...func(*KafkaProducer)) (*KafkaProducer, error) {
	schemaRegistryCfg := schemaregistry.NewConfig(cfg.SchemaRegistryURL)
	schemaRegistryClient, err := schemaregistry.NewClient(schemaRegistryCfg)
	if err != nil {
		return nil, err
	}

	ser, err := protobuf.NewSerializer(schemaRegistryClient, serde.ValueSerde, protobuf.NewSerializerConfig())
	if err != nil {
		return nil, err
	}

	producer, err := kafka.NewProducer(&kafka.ConfigMap{
		"bootstrap.servers": cfg.BootstrapServers,
		"client.id":         cfg.ClientID,
		"acks":              cfg.Acks,
	})

	if err != nil {
		return nil, err
	}

	wrappedProducer := &KafkaProducer{
		config:                  cfg,
		producer:                producer,
		serializer:              ser,
		onMessageDelivered:      nil,
		onMessageDeliveryFailed: nil,
	}

	for _, opt := range opts {
		opt(wrappedProducer)
	}

	go wrappedProducer.startDeliveryReportRoutine()
	return wrappedProducer, nil
}

func (producer *KafkaProducer) startDeliveryReportRoutine() {
	go func() {
		for e := range producer.producer.Events() {
			switch ev := e.(type) {
			case *kafka.Message:
				if ev.TopicPartition.Error != nil {
					if producer.onMessageDeliveryFailed == nil {
						continue
					}
					producer.onMessageDeliveryFailed(*ev)
				} else {
					if producer.onMessageDelivered == nil {
						continue
					}
					producer.onMessageDelivered(*ev)
				}
			}
		}
	}()
}

func (producer *KafkaProducer) ProduceSync(topic string, key string, message proto.Message) error {
	deliveryCh := make(chan kafka.Event, 10000)
	defer close(deliveryCh)

	data, err := producer.serializer.Serialize(topic, message)
	if err != nil {
		return err
	}

	err = producer.producer.Produce(&kafka.Message{
		TopicPartition: kafka.TopicPartition{
			Topic:     &topic,
			Partition: kafka.PartitionAny,
		},
		Key:   []byte(key),
		Value: data,
	}, deliveryCh)
	if err != nil {
		return err
	}

	e := <-deliveryCh
	m := e.(*kafka.Message)

	return m.TopicPartition.Error
}
