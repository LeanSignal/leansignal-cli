package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
)

// PageMeta mirrors lean-api's list envelope metadata.
type PageMeta struct {
	Page    int `json:"page"`
	PerPage int `json:"per_page"`
	Total   int `json:"total"`
}

// Page is lean-api's list envelope: {items, meta}.
type Page[T any] struct {
	Items []T      `json:"items"`
	Meta  PageMeta `json:"meta"`
}

// ListOptions are the query parameters every collection endpoint accepts.
type ListOptions struct {
	Page    int
	PerPage int
	Search  string
	OrderBy string
	Order   string
	// All follows pagination until every item has been fetched.
	All bool
	// Extra carries resource-specific filters (e.g. type=log).
	Extra url.Values
}

// maxPerPage is the largest page leanctl asks for when --all is set. Kept
// modest so one runaway list cannot allocate an unbounded response.
const maxPerPage = 200

// Values renders the options as a query string.
func (o ListOptions) Values() url.Values {
	q := url.Values{}

	for k, vals := range o.Extra {
		for _, v := range vals {
			q.Add(k, v)
		}
	}

	if o.Page > 0 {
		q.Set("page", strconv.Itoa(o.Page))
	}

	if o.PerPage > 0 {
		q.Set("per_page", strconv.Itoa(o.PerPage))
	}

	if o.Search != "" {
		q.Set("search", o.Search)
	}

	if o.OrderBy != "" {
		q.Set("order_by", o.OrderBy)
	}

	if o.Order != "" {
		q.Set("order", o.Order)
	}

	return q
}

// List fetches one page — or every page when opts.All is set — and returns both
// the decoded items and a JSON envelope suitable for `-o json`.
//
// With --all the returned raw bytes are a re-encoded envelope covering all
// items, so scripted output stays a single well-formed document.
func List[T any](ctx context.Context, c *Client, path string, opts ListOptions) (*Page[T], []byte, error) {
	if !opts.All {
		var page Page[T]

		raw, err := c.GetInto(ctx, path, opts.Values(), &page)
		if err != nil {
			return nil, nil, err
		}

		return &page, raw, nil
	}

	acc := &Page[T]{Items: []T{}}
	opts.Page = 1

	if opts.PerPage <= 0 || opts.PerPage > maxPerPage {
		opts.PerPage = maxPerPage
	}

	for {
		var page Page[T]
		if _, err := c.GetInto(ctx, path, opts.Values(), &page); err != nil {
			return nil, nil, err
		}

		acc.Items = append(acc.Items, page.Items...)
		acc.Meta = PageMeta{Page: 1, PerPage: len(acc.Items), Total: page.Meta.Total}

		if len(page.Items) == 0 || len(acc.Items) >= page.Meta.Total {
			break
		}

		opts.Page++
	}

	raw, err := json.Marshal(acc)
	if err != nil {
		return nil, nil, fmt.Errorf("encoding paged result: %w", err)
	}

	return acc, raw, nil
}
