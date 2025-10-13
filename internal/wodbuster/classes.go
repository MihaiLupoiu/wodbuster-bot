package wodbuster

import (
	"context"
	"fmt"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/chromedp"
)

// GetAvailableClasses retrieves all available classes for booking
// day: Day abbreviation string (e.g., "L" for Monday, "X" for Wednesday)
func (c *Client) GetAvailableClasses(email, password string, day string) ([]ClassSchedule, error) {
	c.logger.Info("Getting available classes", "day", day)

	actions := login(c.baseURL, email, password)
	actions = append(actions, rememberBrowser()...)
	actions = append(actions, getAvailableClasses(false)...)

	if day != "" {
		actions = append(actions, selectDay(day)...)
	}

	actions = append(actions,
		// Wait for the classes to load
		chromedp.Sleep(2*time.Second))

	if err := chromedp.Run(c.ctx, actions...); err != nil {
		return nil, fmt.Errorf("failed to navigate: %w", err)
	}

	// Parse available classes from the page
	var classes []ClassSchedule
	err := chromedp.Run(c.ctx,
		chromedp.ActionFunc(func(ctx context.Context) error {
			var nodes []*cdp.Node
			if err := chromedp.Nodes(`//div[contains(@class, 'clase')]`, &nodes, chromedp.BySearch).Do(ctx); err != nil {
				return err
			}

			for _, node := range nodes {
				class, err := parseClassNode(ctx, node)
				if err != nil {
					c.logger.Warn("Failed to parse class node", "error", err)
					continue
				}
				if class != nil {
					classes = append(classes, *class)
				}
			}
			return nil
		}),
	)

	if err != nil {
		return nil, fmt.Errorf("failed to parse classes: %w", err)
	}

	c.logger.Info("Found available classes", "count", len(classes))
	return classes, nil
}

// getAvailableClasses navigates to the class booking page
func getAvailableClasses(nextWeek bool) []chromedp.Action {

	actions := []chromedp.Action{
		// Click the link
		chromedp.Click(`//a[contains(text(), 'Reservar clases')]`),
		// Wait for form elements to be present
		chromedp.WaitVisible(`//div[@id="calendar"]`),
	}
	if nextWeek {
		actions = append(actions, chromedp.Click(`a.next.icon`, chromedp.ByQuery))
	}

	return actions
}

// parseClassNode extracts class information from a DOM node
func parseClassNode(ctx context.Context, node *cdp.Node) (*ClassSchedule, error) {
	if node == nil {
		return nil, fmt.Errorf("node is nil")
	}

	var classTypeStr, hour string
	var hasReservarButton bool

	// Get class type from h3.entrenamiento
	if err := chromedp.Text(`.//h3[contains(@class, 'entrenamiento')]`, &classTypeStr, chromedp.BySearch, chromedp.FromNode(node)).Do(ctx); err != nil {
		return nil, fmt.Errorf("failed to get class type: %w", err)
	}

	// Get hour from div.hora
	if err := chromedp.Text(`.//div[@class='hora']`, &hour, chromedp.BySearch, chromedp.FromNode(node)).Do(ctx); err != nil {
		return nil, fmt.Errorf("failed to get hour: %w", err)
	}

	// Check if "Reservar" button exists (class is available)
	var nodes []*cdp.Node
	if err := chromedp.Nodes(`.//button[contains(@class, 'entrenar') and contains(., 'Reservar')]`, &nodes, chromedp.BySearch, chromedp.FromNode(node)).Do(ctx); err == nil {
		hasReservarButton = len(nodes) > 0
	}

	// Clean up class type (remove extra whitespace and asterisks)
	classTypeStr = cleanClassType(classTypeStr)

	return &ClassSchedule{
		Day:       "", // Day needs to be set from context
		Hour:      hour,
		ClassType: ClassType(classTypeStr),
		Available: hasReservarButton,
	}, nil
}
