package selector

import "k8s.io/apimachinery/pkg/labels"

type labelSelector = interface {
	Matches(map[string]string) bool
	String() string
}

type K8sSelectorParser struct{}

func (K8sSelectorParser) Parse(selector string) (labelSelector, error) {
	lq, err := labels.Parse(selector)
	if err != nil {
		return nil, err
	}

	return &k8sSelector{lq}, nil
}

type k8sSelector struct {
	lq labels.Selector
}

func (s *k8sSelector) Matches(m map[string]string) bool {
	return s.lq.Matches(labels.Set(m))
}

func (s *k8sSelector) String() string {
	return s.lq.String()
}
