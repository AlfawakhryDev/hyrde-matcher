// Package store keeps engineers and their track record in DynamoDB.
package store

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/AlfawakhryDev/hyrde-matcher/internal/events"
	"github.com/AlfawakhryDev/hyrde-matcher/internal/match"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// ErrUnknownEngineer means an event named an engineer the table does not have.
var ErrUnknownEngineer = errors.New("unknown engineer")

// Store talks to two tables: engineers (one item per engineer, profile and
// counters together) and processed events (one item per event already
// applied, expiring after a week).
type Store struct {
	db        *dynamodb.Client
	engineers string
	processed string
}

// New connects to DynamoDB. endpoint is for DynamoDB Local; empty means AWS.
func New(ctx context.Context, region, endpoint, tablePrefix string) (*Store, error) {
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return nil, err
	}
	db := dynamodb.NewFromConfig(cfg, func(o *dynamodb.Options) {
		if endpoint != "" {
			o.BaseEndpoint = aws.String(endpoint)
		}
	})
	return &Store{db: db, engineers: tablePrefix + "engineers", processed: tablePrefix + "processed_events"}, nil
}

// Engineers reads the whole engineers table, page by page. It runs in the
// background refresher, never on a request.
// ponytail: a full scan every few seconds is fine for thousands of engineers;
// past that, read only rows changed since the last refresh (a stream or a GSI).
func (s *Store) Engineers(ctx context.Context) ([]match.Engineer, error) {
	var out []match.Engineer
	pages := dynamodb.NewScanPaginator(s.db, &dynamodb.ScanInput{TableName: &s.engineers})
	for pages.HasMorePages() {
		page, err := pages.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("scan engineers: %w", err) // %w wraps, so callers can errors.Is it
		}
		var batch []match.Engineer
		if err := attributevalue.UnmarshalListOfMaps(page.Items, &batch); err != nil {
			return nil, fmt.Errorf("decode engineers: %w", err)
		}
		out = append(out, batch...)
	}
	return out, nil
}

// PutEngineer writes a profile (used by the seed tool and onboarding).
func (s *Store) PutEngineer(ctx context.Context, e match.Engineer) error {
	item, err := attributevalue.MarshalMap(e)
	if err != nil {
		return err
	}
	_, err = s.db.PutItem(ctx, &dynamodb.PutItemInput{TableName: &s.engineers, Item: item})
	return err
}

func num(n int) types.AttributeValue { return &types.AttributeValueMemberN{Value: strconv.Itoa(n)} }

// Apply adds an event's effect to an engineer's counters exactly once.
//
// One transaction does both writes: record the event id (failing if it is
// already there) and update the counters (failing if the engineer is
// unknown). Either both happen or neither does, so a redelivered event is a
// no-op instead of a double count. applied is false for a duplicate.
func (s *Store) Apply(ctx context.Context, e events.Event) (applied bool, err error) {
	d, ok := e.Delta()
	if !ok {
		return false, nil
	}
	expires := strconv.FormatInt(time.Now().Add(7*24*time.Hour).Unix(), 10)
	_, err = s.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{
		TransactItems: []types.TransactWriteItem{
			{Put: &types.Put{
				TableName: &s.processed,
				Item: map[string]types.AttributeValue{
					"event_id":   &types.AttributeValueMemberS{Value: e.ID},
					"expires_at": &types.AttributeValueMemberN{Value: expires}, // DynamoDB TTL deletes it later
				},
				ConditionExpression: aws.String("attribute_not_exists(event_id)"),
			}},
			{Update: &types.Update{
				TableName:           &s.engineers,
				Key:                 map[string]types.AttributeValue{"engineer_id": &types.AttributeValueMemberS{Value: e.EngineerID}},
				UpdateExpression:    aws.String("ADD active :a, delivered :d, on_time :o, reviewed :r, approved :p"),
				ConditionExpression: aws.String("attribute_exists(engineer_id)"),
				ExpressionAttributeValues: map[string]types.AttributeValue{
					":a": num(d.Active), ":d": num(d.Delivered), ":o": num(d.OnTime), ":r": num(d.Reviewed), ":p": num(d.Approved),
				},
			}},
		},
	})
	if err == nil {
		return true, nil
	}
	// errors.As finds a specific error type anywhere in the wrapped chain.
	var cancelled *types.TransactionCanceledException
	if errors.As(err, &cancelled) && len(cancelled.CancellationReasons) == 2 {
		if aws.ToString(cancelled.CancellationReasons[0].Code) == "ConditionalCheckFailed" {
			return false, nil // seen before: already counted
		}
		if aws.ToString(cancelled.CancellationReasons[1].Code) == "ConditionalCheckFailed" {
			return false, ErrUnknownEngineer
		}
	}
	return false, err
}

// EnsureTables creates both tables if missing. On-demand billing: no
// capacity to plan, you pay per request.
func (s *Store) EnsureTables(ctx context.Context) error {
	for table, key := range map[string]string{s.engineers: "engineer_id", s.processed: "event_id"} {
		_, err := s.db.CreateTable(ctx, &dynamodb.CreateTableInput{
			TableName:            aws.String(table),
			BillingMode:          types.BillingModePayPerRequest,
			AttributeDefinitions: []types.AttributeDefinition{{AttributeName: aws.String(key), AttributeType: types.ScalarAttributeTypeS}},
			KeySchema:            []types.KeySchemaElement{{AttributeName: aws.String(key), KeyType: types.KeyTypeHash}},
		})
		var exists *types.ResourceInUseException
		if err != nil && !errors.As(err, &exists) {
			return fmt.Errorf("create %s: %w", table, err)
		}
	}
	// Expire processed-event ids after a week. Enabling TTL twice is a
	// ValidationException and DynamoDB Local ignores TTL, so neither is fatal.
	_, _ = s.db.UpdateTimeToLive(ctx, &dynamodb.UpdateTimeToLiveInput{
		TableName:               &s.processed,
		TimeToLiveSpecification: &types.TimeToLiveSpecification{AttributeName: aws.String("expires_at"), Enabled: aws.Bool(true)},
	})
	return nil
}
