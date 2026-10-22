package vcenter

import (
	"context"
	"fmt"
	"time"

	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/vapi/rest"
	"github.com/vmware/govmomi/vapi/tags"
)

const (
	tagCategoryDescription = "Managed by vmbootstrap"
	tagDescription         = "Managed by vmbootstrap"
	tagCardinalityMultiple = "MULTIPLE"
	tagCardinalitySingle   = "SINGLE"
	tagAssociableVM        = "VirtualMachine"
)

// TagRef names a vSphere tag by its category and tag name.
type TagRef struct {
	Category string
	Tag      string
}

// logoutTimeout bounds the REST session logout (it must not depend on a possibly cancelled ctx).
const logoutTimeout = 10 * time.Second

// EnsureVMTags finds or creates each category and tag, then attaches the tag to
// the VM unless it is already attached. Safe to call repeatedly. Tagging lives in
// the vSphere Automation REST API, so a separate session is opened and closed.
// The returned warnings describe tags that were skipped on purpose (e.g. the
// category is SINGLE cardinality and the VM already carries another tag of it).
func (c *Client) EnsureVMTags(vm *object.VirtualMachine, refs []TagRef) ([]string, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	rc := rest.NewClient(c.conn.Client)
	if err := rc.Login(c.ctx, c.user); err != nil {
		return nil, fmt.Errorf("vSphere REST login failed: %w", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), logoutTimeout)
		defer cancel()
		_ = rc.Logout(ctx)
	}()
	return ensureVMTags(c.ctx, tags.NewManager(rc), vm, refs)
}

func ensureVMTags(ctx context.Context, m *tags.Manager, vm *object.VirtualMachine, refs []TagRef) ([]string, error) {
	attached, err := m.GetAttachedTags(ctx, vm)
	if err != nil {
		return nil, fmt.Errorf("failed to list tags attached to VM: %w", err)
	}
	hasTag := make(map[string]bool, len(attached))
	hasCategory := make(map[string]bool, len(attached))
	for _, t := range attached {
		hasTag[t.ID] = true
		hasCategory[t.CategoryID] = true
	}
	cats, err := m.GetCategories(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list tag categories: %w", err)
	}

	var warnings []string
	for _, ref := range refs {
		var cat *tags.Category
		for i := range cats {
			if cats[i].Name == ref.Category {
				cat = &cats[i]
				break
			}
		}
		if cat == nil {
			id, err := m.CreateCategory(ctx, &tags.Category{
				Name:            ref.Category,
				Description:     tagCategoryDescription,
				Cardinality:     tagCardinalityMultiple,
				AssociableTypes: []string{tagAssociableVM},
			})
			if err != nil {
				return warnings, fmt.Errorf("failed to create tag category %q: %w", ref.Category, err)
			}
			cats = append(cats, tags.Category{ID: id, Name: ref.Category, Cardinality: tagCardinalityMultiple})
			cat = &cats[len(cats)-1]
		} else if !categoryAllowsVM(cat) {
			return warnings, fmt.Errorf("tag category %q cannot be associated with VirtualMachine objects", ref.Category)
		}

		tagID, err := ensureTag(ctx, m, cat, ref)
		if err != nil {
			return warnings, err
		}
		if hasTag[tagID] {
			continue
		}
		if cat.Cardinality == tagCardinalitySingle && hasCategory[cat.ID] {
			warnings = append(warnings, fmt.Sprintf("tag %s/%s not attached: category is SINGLE cardinality and the VM already has a tag from it", ref.Category, ref.Tag))
			continue
		}
		if err := m.AttachTag(ctx, tagID, vm); err != nil {
			return warnings, fmt.Errorf("failed to attach tag %s/%s: %w", ref.Category, ref.Tag, err)
		}
		hasTag[tagID] = true
		hasCategory[cat.ID] = true
	}
	return warnings, nil
}

// ensureTag returns the ID of the tag in cat, creating it when missing.
func ensureTag(ctx context.Context, m *tags.Manager, cat *tags.Category, ref TagRef) (string, error) {
	existing, err := m.GetTagsForCategory(ctx, cat.ID)
	if err != nil {
		return "", fmt.Errorf("failed to list tags of category %q: %w", ref.Category, err)
	}
	for _, t := range existing {
		if t.Name == ref.Tag {
			return t.ID, nil
		}
	}
	id, err := m.CreateTag(ctx, &tags.Tag{Name: ref.Tag, Description: tagDescription, CategoryID: cat.ID})
	if err != nil {
		return "", fmt.Errorf("failed to create tag %s/%s: %w", ref.Category, ref.Tag, err)
	}
	return id, nil
}

// categoryAllowsVM reports whether the category accepts VirtualMachine objects
// (an empty AssociableTypes list means every type).
func categoryAllowsVM(c *tags.Category) bool {
	if len(c.AssociableTypes) == 0 {
		return true
	}
	for _, k := range c.AssociableTypes {
		if k == tagAssociableVM {
			return true
		}
	}
	return false
}
