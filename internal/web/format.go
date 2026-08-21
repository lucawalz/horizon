package web

import (
	"time"

	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/duration"
)

const (
	absent    = "-"
	expired   = "expired"
	stampForm = "2006-01-02 15:04:05 MST"
)

func age(at *metav1.Time, now time.Time) string {
	if at.IsZero() {
		return absent
	}
	elapsed := now.Sub(at.Time)
	if elapsed < 0 {
		elapsed = 0
	}
	return duration.HumanDuration(elapsed)
}

func remaining(at *metav1.Time, now time.Time) string {
	if at.IsZero() {
		return absent
	}
	left := at.Sub(now)
	if left <= 0 {
		return expired
	}
	return duration.HumanDuration(left)
}

func stamp(at *metav1.Time, now time.Time) string {
	if at.IsZero() {
		return absent
	}
	return at.UTC().Format(stampForm) + " (" + age(at, now) + ")"
}

func conditionStatus(conditions []metav1.Condition, name string) string {
	for i := range conditions {
		if conditions[i].Type == name {
			return string(conditions[i].Status)
		}
	}
	return absent
}

func text(value string) string {
	if value == "" {
		return absent
	}
	return value
}

func bytesQuantity(count int64) string {
	if count <= 0 {
		return absent
	}
	return resource.NewQuantity(count, resource.BinarySI).String()
}
