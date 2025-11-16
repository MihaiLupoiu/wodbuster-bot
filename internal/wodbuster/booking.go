package wodbuster

import (
	"context"
	"fmt"
	"time"

	"github.com/chromedp/chromedp"
)

// BookClass books a class for a specific day, class type, and hour
// day: Day abbreviation string (e.g., "L", "M", "X", "J", "V", "S", "D")
//
//	You can use the Day constants (DayMonday, DayTuesday, etc.) or plain strings
//
// classType: Class type string (e.g., "Wod", "Open box", "HYROX")
//
//	You can use the ClassType constants (ClassTypeWod, ClassTypeOpenBox, etc.) or plain strings
//
// hour: Time in format "HH:MM" (e.g., "07:00", "19:30")
func (c *Client) BookClass(_ context.Context, email, password string, day, classType, hour string) error {
	if day == "" || classType == "" || hour == "" {
		return fmt.Errorf("day, classType, and hour are required")
	}

	c.logger.Info("Starting class booking",
		"day", day,
		"classType", classType,
		"hour", hour)

	actions := login(c.baseURL, email, password)
	actions = append(actions, notRememberBrowser()...)
	actions = append(actions, getAvailableClasses(true)...) // false for current week
	actions = append(actions, selectDay(day)...)
	actions = append(actions, bookClass(classType, hour)...)
	actions = append(actions, acceptConfirmation()...)
	actions = append(actions,
		// Wait for the confirmation to process
		chromedp.Sleep(3*time.Second))

	if err := chromedp.Run(c.ctx, actions...); err != nil {
		c.logger.Error("Failed to book class",
			"error", err,
			"day", day,
			"classType", classType,
			"hour", hour)
		return fmt.Errorf("failed to book class: %w", err)
	}

	c.logger.Info("Successfully booked class",
		"day", day,
		"classType", classType,
		"hour", hour)

	return nil
}

// RemoveBooking removes a booking for a specific day and hour (not implemented yet)
func (c *Client) RemoveBooking(day, hour string) error {
	if err := chromedp.Run(c.ctx,
		chromedp.Navigate(c.baseURL+"/schedule"),
	); err != nil {
		return fmt.Errorf("failed to navigate: %w", err)
	}
	return ErrNotImplemented
}

// bookClass finds and clicks the "Reservar" button for a specific class type and hour
// classType examples: "Wod", "Open box", "HYROX", etc.
// hour examples: "07:00", "08:00", "19:30", etc.
func bookClass(classType string, hour string) []chromedp.Action {
	// Construct XPath to find the "Reservar" button based on both class type and hour
	// Structure: div.clase > div.entrenamientoHead > div.namehour > (h3.entrenamiento + div.hora)
	// Then find the button in div.actionsjs > button.entrenar
	xpathReserve := fmt.Sprintf(
		`//div[contains(@class, 'clase')]//div[@class='namehour'][.//h3[contains(@class, 'entrenamiento') and contains(normalize-space(text()), '%s')] and .//div[@class='hora' and text()='%s']]/ancestor::div[contains(@class, 'clase')]//button[contains(@class, 'entrenar') and contains(., 'Reservar')]`,
		classType, hour,
	)

	return []chromedp.Action{
		// Wait for the "Reservar" button to appear
		chromedp.WaitVisible(xpathReserve, chromedp.BySearch),
		// Click the "Reservar" button
		chromedp.Click(xpathReserve, chromedp.BySearch),

		chromedp.Sleep(100 * time.Millisecond),
	}
}

// acceptConfirmation clicks the "Aceptar" button in the confirmation dialog
func acceptConfirmation() []chromedp.Action {
	// XPath for the "Aceptar" button inside the confirmation dialog
	xpath := `//div[h4[text()='Confirmación Requerida']]//button[contains(@class, 'button small radius') and text()='Aceptar']`

	return []chromedp.Action{
		// Wait for the confirmation dialog to appear
		chromedp.WaitVisible(xpath),
		// Click the "Aceptar" button
		chromedp.Click(xpath),
		chromedp.Sleep(100 * time.Millisecond),
	}
}

// selectDay clicks on a specific day in the calendar
func selectDay(day string) []chromedp.Action {
	// Construct XPath based on the day abbreviation (L, M, X, J, V, S, D)
	xpath := fmt.Sprintf(`//a[@class="dia" or contains(@class, "current")]/span[text()='%s']/parent::a`, day)

	return []chromedp.Action{
		chromedp.Sleep(1 * time.Second),

		// Wait for the day to be visible
		chromedp.WaitVisible(xpath),
		// Click on the specified day
		chromedp.Click(xpath),
	}
}

func (c *Client) BookClassOnly(day, classType, hour string) error {
	if day == "" || classType == "" || hour == "" {
		return fmt.Errorf("day, classType, and hour are required")
	}

	c.logger.Info("Starting class booking",
		"day", day,
		"classType", classType,
		"hour", hour)

	actions := append([]chromedp.Action{}, bookClass(classType, hour)...)
	actions = append(actions, acceptConfirmation()...)
	actions = append(actions,
		chromedp.Sleep(1*time.Second))

	if err := chromedp.Run(c.ctx, actions...); err != nil {
		c.logger.Error("Failed to book class",
			"error", err,
			"day", day,
			"classType", classType,
			"hour", hour)
		return fmt.Errorf("failed to book class: %w", err)
	}

	c.logger.Info("Successfully booked class",
		"day", day,
		"classType", classType,
		"hour", hour)

	return nil
}
