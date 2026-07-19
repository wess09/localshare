package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type Route struct {
	ent.Schema
}

func (Route) Fields() []ent.Field {
	return []ent.Field{
		field.String("token").Unique().NotEmpty(),
		field.String("node_id").NotEmpty(),
		field.String("target_url").NotEmpty(),
		field.String("public_url").NotEmpty(),
		field.String("peer_id").Default(""),
		field.String("status").Default("active"),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
		field.Time("expires_at"),
	}
}

func (Route) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("node_id"),
		index.Fields("status", "expires_at"),
	}
}
