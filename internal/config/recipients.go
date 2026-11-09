package config

import (
	"errors"
	"net/mail"
	"strings"
)

func ParseRecipients(raw string) ([]*mail.Address, error) {
	if len(raw) > 16<<10 || strings.ContainsRune(raw, 0) {
		return nil, errors.New("目标邮箱列表过长或包含无效字符")
	}
	if single, err := ParseAddress(raw); err == nil && len(raw) <= 320 {
		return []*mail.Address{single}, nil
	}
	lineBreak := strings.NewReplacer("\r\n", "\n", "\r", "\n")
	separator := strings.NewReplacer("；", ",", "，", ",", ";", ",")
	var recipients []*mail.Address
	seen := make(map[string]bool)
	for _, line := range strings.Split(lineBreak.Replace(raw), "\n") {
		line = strings.Trim(strings.TrimSpace(line), ",")
		if strings.TrimSpace(line) == "" {
			continue
		}
		addresses, err := mail.ParseAddressList(line)
		if err != nil {
			addresses, err = mail.ParseAddressList(strings.Trim(separator.Replace(line), ","))
		}
		if err != nil {
			return nil, errors.New("目标邮箱格式无效，请每行填写一个邮箱，或用逗号分隔")
		}
		for _, address := range addresses {
			if len(address.String()) > 320 {
				return nil, errors.New("单个目标邮箱地址过长")
			}
			key := strings.ToLower(address.Address)
			if !seen[key] {
				seen[key] = true
				recipients = append(recipients, address)
			}
		}
	}
	if len(recipients) == 0 || len(recipients) > 50 {
		return nil, errors.New("请填写 1 至 50 个目标邮箱")
	}
	return recipients, nil
}
