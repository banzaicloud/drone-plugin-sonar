package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"text/template"
	"regexp"
	"bytes"
	"time"
	"errors"

	"github.com/Sirupsen/logrus"
)

type Plugin struct {
	Host       string
	Token      string
	Key        string
	Name       string
	Version    string
	Sources    string
	Inclusions string
	Exclusions string
	Language   string
	Profile    string
	Encoding   string
	Remote     string
	Branch     string
	Quality    string
}

func (p Plugin) buildScannerProperties() error {

	p.Key = strings.Replace(p.Key, "/", ":", -1)

	tmpl, err := template.ParseFiles("/opt/sonar-scanner/conf/sonar-scanner.properties.tmpl")
	if err != nil {
		logrus.WithFields(logrus.Fields{
			"error": err,
		}).Fatal("Template parsing failed")
	}

	f, err := os.Create("/opt/sonar-scanner/conf/sonar-scanner.properties")
	defer f.Close()
	if err != nil {
		logrus.WithFields(logrus.Fields{
			"error": err,
		}).Fatal("sonar-properties file creation failed")
	}

	err = tmpl.ExecuteTemplate(f, "sonar-scanner.properties.tmpl", p)
	if err != nil {
		logrus.WithFields(logrus.Fields{
			"error": err,
		}).Fatal("Template execution failed")
	}

	return nil
}

func (p Plugin) Exec() error {

	err := p.buildScannerProperties()
	if err != nil {
		return err
	}

	j := staticScan()
	logrus.WithFields(logrus.Fields{
		"job url": j,	
	}).Info("Job url")

	_, err = waitForSonarJob(j)
	if err != nil {
		logrus.WithFields(logrus.Fields{
			"error": err,
		}).Fatal("Unable to get Job state")
	}

	p.Key = strings.Replace(p.Key, "/", ":", -1)
	p.Key += ":" + p.Branch
	body := getStatus(p.Host, p.Key)

	if s := checkStatus(body); s != p.Quality || s != "OK" {
		logrus.WithFields(logrus.Fields{
			"status": s,
		}).Fatal("QualityGate status failed")
	}

	return nil
}

func staticScan() string {
	cmd := exec.Command("sonar-scanner")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		logrus.WithFields(logrus.Fields{
			"error": err,
		}).Fatal("Run command failed")
	}
	outStr := string(stdout.Bytes())
	fmt.Printf("out:\n%s", outStr)

	jobId := parseOutput(outStr)

	return jobId
}

func getStatus(sonarUrl string, Key string) []byte {

	sonarUrl += "/api/qualitygates/project_status"
	payload := strings.NewReader("projectKey=" + Key)

	req, _ := http.NewRequest("POST", sonarUrl, payload)

	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Add("Cache-Control", "no-cache")

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		logrus.WithFields(logrus.Fields{
			"error": err,
		}).Fatal("Failed get status")
	}

	defer res.Body.Close()
	body, _ := ioutil.ReadAll(res.Body)

	return body
}

func checkStatus(b []byte) string {
	type responseStr struct {
		ProjectStatus struct {
			Status     string `json:"status"`
			Conditions []struct {
				Status         string `json:"status"`
				MetricKey      string `json:"metricKey"`
				Comparator     string `json:"comparator"`
				PeriodIndex    int    `json:"periodIndex"`
				ErrorThreshold string `json:"errorThreshold"`
				ActualValue    string `json:"actualValue"`
			} `json:"conditions"`
			Periods []struct {
				Index     int    `json:"index"`
				Mode      string `json:"mode"`
				Date      string `json:"date"`
				Parameter string `json:"parameter"`
			} `json:"periods"`
			IgnoredConditions bool `json:"ignoredConditions"`
		} `json:"projectStatus"`
	}

	data := responseStr{}
	if err := json.Unmarshal(b, &data); err != nil {
		logrus.WithFields(logrus.Fields{
			"error": err,
		}).Fatal("Failed")
	}

	logrus.WithFields(logrus.Fields{
		"data": data,
	}).Info("QualityGate json data")

	return data.ProjectStatus.Status
}

func parseOutput(outs string) string {
	var jobId = regexp.MustCompile(`https?:\/\/.*\/api\/ce\/task\?id=.*`)
	result := jobId.FindStringSubmatch(outs)

	return result[0]
}

func checkSonarJobStatus(b []byte) string {

	type jobStatus struct {
		Task struct {
			ID                 string `json:"id"`
			Type               string `json:"type"`
			ComponentID        string `json:"componentId"`
			ComponentKey       string `json:"componentKey"`
			ComponentName      string `json:"componentName"`
			ComponentQualifier string `json:"componentQualifier"`
			AnalysisID         string `json:"analysisId"`
			Status             string `json:"status"`
			SubmittedAt        string `json:"submittedAt"`
			SubmitterLogin     string `json:"submitterLogin"`
			StartedAt          string `json:"startedAt"`
			ExecutedAt         string `json:"executedAt"`
			ExecutionTimeMs    int    `json:"executionTimeMs"`
			Logs               bool   `json:"logs"`
			HasScannerContext  bool   `json:"hasScannerContext"`
			Organization       string `json:"organization"`
		} `json:"task"`
	}

	status := jobStatus{}
	if err := json.Unmarshal(b, &status); err != nil {
		logrus.WithFields(logrus.Fields{
			"error": err,
		}).Fatal("Failed")
	}

	logrus.WithFields(logrus.Fields{
		"data": status.Task.Status,
	}).Info("Sonar job status")
	
	return status.Task.Status
}

func getSonarJobStatus(j string) []byte {

	req, _ := http.NewRequest("GET", j, nil)
	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Add("Cache-Control", "no-cache")

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		logrus.WithFields(logrus.Fields{
			"error": err,
		}).Fatal("Failed get sonar job status")
	}

	defer res.Body.Close()
	body, _ := ioutil.ReadAll(res.Body)

	return body
}

func waitForSonarJob(j string) (bool, error) {
	timeout := time.After(5 * time.Second)
	tick := time.Tick(500 * time.Millisecond)
	for {
		select {
		case <-timeout:
			return false, errors.New("timed out")
		case <-tick:
			b := getSonarJobStatus(j)
			status := checkSonarJobStatus(b)
			if status == "SUCCESS" {
				return true, nil
			}
		}
	}
}