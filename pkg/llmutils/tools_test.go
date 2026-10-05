package llmutils

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func fieldPaths(fields []ContentField) []string {
	out := make([]string, len(fields))
	for i, f := range fields {
		out[i] = f.Path
	}
	return out
}

func fieldValue(t *testing.T, fields []ContentField, path string) string {
	t.Helper()
	for _, f := range fields {
		if f.Path == path {
			return f.Value
		}
	}
	require.FailNowf(t, "field not extracted", "path %q; got %v", path, fieldPaths(fields))
	return ""
}

func TestCollectToolDefinitionFields(t *testing.T) {
	t.Run("description and schema text keywords are extracted", func(t *testing.T) {
		// Everything the model reads in a tool definition is prompt text: a
		// default e-mail or an enum of real names reaches the provider exactly
		// like message content does.
		tool := gjson.Parse(`{
			"name":"create_deal",
			"description":"Create a deal, notify a@b.com",
			"input_schema":{
				"type":"object",
				"title":"Deal",
				"properties":{
					"manager":{"type":"string","default":"a@b.com","description":"owner e-mail"},
					"client":{"type":"string","enum":["John Smith","Jane Roe"]},
					"inn":{"type":"string","examples":["7736050003"]}
				},
				"required":["client"]
			}
		}`)

		fields := CollectToolDefinitionFields(tool, "tools.0", "input_schema")

		assert.ElementsMatch(t, []string{
			"tools.0.description",
			"tools.0.input_schema.title",
			"tools.0.input_schema.properties.manager.default",
			"tools.0.input_schema.properties.manager.description",
			"tools.0.input_schema.properties.client.enum.0",
			"tools.0.input_schema.properties.client.enum.1",
			"tools.0.input_schema.properties.inn.examples.0",
		}, fieldPaths(fields))
		assert.Equal(t, "Create a deal, notify a@b.com", fieldValue(t, fields, "tools.0.description"))
		assert.Equal(t, "a@b.com", fieldValue(t, fields, "tools.0.input_schema.properties.manager.default"))
		assert.Equal(t, "Jane Roe", fieldValue(t, fields, "tools.0.input_schema.properties.client.enum.1"))
	})

	t.Run("name and machine-facing keywords are never extracted", func(t *testing.T) {
		// The model echoes `name` back in tool_use.name / tool_calls[].function.name,
		// which are not demasked — masking it would break tool calling. `type`,
		// `format`, `pattern` and `required` are contract, not prose.
		tool := gjson.Parse(`{
			"name":"send_mail",
			"input_schema":{
				"type":"object",
				"properties":{"to":{"type":"string","format":"email","pattern":"^.+@.+$"}},
				"required":["to"],
				"additionalProperties":false
			}
		}`)

		assert.Empty(t, CollectToolDefinitionFields(tool, "tools.0", "input_schema"))
	})

	t.Run("operational fields of server-side tools are left alone", func(t *testing.T) {
		// A hosted/MCP tool's server_url, headers and user_location are consumed
		// by the provider to perform the call; a placeholder there breaks the
		// tool instead of protecting anything.
		tool := gjson.Parse(`{
			"type":"mcp",
			"server_label":"acme",
			"server_url":"https://mcp.acme.internal/sse",
			"headers":{"Authorization":"Bearer sk-acme-0123456789"},
			"user_location":{"type":"approximate","city":"Berlin"}
		}`)

		assert.Empty(t, CollectToolDefinitionFields(tool, "tools.0", "parameters"))
	})

	t.Run("extra description keys are collected in the order given", func(t *testing.T) {
		// The Responses dialect passes server_description alongside description:
		// both are model-visible prose on an MCP tool, and the order fixes their
		// placeholder numbering.
		tool := gjson.Parse(`{
			"type":"mcp",
			"server_url":"https://mcp.acme.internal/sse",
			"description":"Acme tools, owner <EMAIL_7>",
			"server_description":"Acme HR server, contact <EMAIL_8>"
		}`)

		fields := CollectToolDefinitionFields(tool, "tools.0", "parameters", "description", "server_description")
		assert.Equal(t, []string{"tools.0.description", "tools.0.server_description"}, fieldPaths(fields))
		assert.Equal(t, "Acme HR server, contact <EMAIL_8>", fieldValue(t, fields, "tools.0.server_description"))

		// The default stays exactly `description`, so the other dialects gain no
		// surprises from a field their API does not define.
		fields = CollectToolDefinitionFields(tool, "tools.0", "parameters")
		assert.Equal(t, []string{"tools.0.description"}, fieldPaths(fields))
	})

	t.Run("a property named like a keyword is treated as a schema", func(t *testing.T) {
		// Under `properties` the keys are property names: a property called
		// "description" must be walked as a subschema, not harvested as text.
		tool := gjson.Parse(`{
			"name":"f",
			"parameters":{
				"type":"object",
				"properties":{
					"description":{"type":"string","description":"free-form note"},
					"enum":{"type":"string","default":"a@b.com"}
				}
			}
		}`)

		fields := CollectToolDefinitionFields(tool, "tools.0", "parameters")

		assert.ElementsMatch(t, []string{
			"tools.0.parameters.properties.description.description",
			"tools.0.parameters.properties.enum.default",
		}, fieldPaths(fields))
	})

	t.Run("nested schema combinators are traversed", func(t *testing.T) {
		tool := gjson.Parse(`{
			"name":"f",
			"parameters":{
				"anyOf":[
					{"type":"object","properties":{"a":{"type":"string","description":"first"}}},
					{"type":"array","items":{"type":"string","default":"second"}}
				],
				"$defs":{"shared":{"type":"string","title":"third"}}
			}
		}`)

		fields := CollectToolDefinitionFields(tool, "tools.0", "parameters")

		assert.ElementsMatch(t, []string{
			"tools.0.parameters.anyOf.0.properties.a.description",
			"tools.0.parameters.anyOf.1.items.default",
			"tools.0.parameters.$defs.shared.title",
		}, fieldPaths(fields))
	})

	t.Run("non-string leaves and empty strings are skipped", func(t *testing.T) {
		// Replacing a number or a bool with a string placeholder would change
		// the JSON type the model is asked to produce.
		tool := gjson.Parse(`{
			"name":"f",
			"description":"",
			"parameters":{"properties":{
				"retries":{"type":"integer","default":3},
				"strict":{"type":"boolean","default":true},
				"note":{"type":"string","description":""}
			}}
		}`)

		assert.Empty(t, CollectToolDefinitionFields(tool, "tools.0", "parameters"))
	})

	t.Run("object-shaped default is collected per string leaf", func(t *testing.T) {
		tool := gjson.Parse(`{
			"name":"f",
			"parameters":{"properties":{"contact":{
				"type":"object",
				"default":{"email":"a@b.com","retries":2}
			}}}
		}`)

		fields := CollectToolDefinitionFields(tool, "tools.0", "parameters")

		require.Len(t, fields, 1)
		assert.Equal(t, "tools.0.parameters.properties.contact.default.email", fields[0].Path)
		assert.Equal(t, "a@b.com", fields[0].Value)
	})

	t.Run("path metacharacters in property names are escaped", func(t *testing.T) {
		tool := gjson.Parse(`{"name":"f","parameters":{"properties":{
			"user.email":{"type":"string","default":"a@b.com"}}}}`)

		fields := CollectToolDefinitionFields(tool, "tools.0", "parameters")

		require.Len(t, fields, 1)
		assert.Equal(t, `tools.0.parameters.properties.user\.email.default`, fields[0].Path)
	})

	t.Run("missing description and schema yield nothing", func(t *testing.T) {
		assert.Empty(t, CollectToolDefinitionFields(gjson.Parse(`{"name":"f"}`), "tools.0", "input_schema"))
		assert.Empty(t, CollectToolDefinitionFields(gjson.Parse(`{}`), "tools.0", "input_schema"))
	})
}
