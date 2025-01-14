//
// Copyright 2022 Red Hat, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"context"
	"io/ioutil"
	"os"
	"path/filepath"
	"sync"

	"io/fs"
	"log"

	"github.com/redhat-appstudio/pvc-cleaner/pkg"
	"github.com/redhat-appstudio/pvc-cleaner/pkg/k8s"
	pipelinev1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1beta1"
	"github.com/tektoncd/pipeline/pkg/client/clientset/versioned"
	"github.com/tektoncd/pipeline/pkg/client/clientset/versioned/typed/pipeline/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	watchapi "k8s.io/apimachinery/pkg/watch"
)

func main() {
	pkg.ParseFlags()
	config := k8s.GetClusterConfig()

	// create Tekton clientset
	tknClientset, err := versioned.NewForConfig(config)
	if err != nil {
		log.Fatalf("failed to create pipeline clientset %s", err)
	}

	namespace, err := k8s.GetNamespace()
	if err != nil {
		log.Fatalf("failed to create pipeline clientset %s", err)
	}

	pipelineRunApi := tknClientset.TektonV1beta1().PipelineRuns(namespace)

	go watchNewPipelineRuns(pipelineRunApi)

	pipelineRuns, err := pipelineRunApi.List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		log.Fatal(err)
	}

	pvcSubPaths, err := ioutil.ReadDir(pkg.SOURCE_VOLUME_DIR)
	if err != nil {
		log.Fatal(err)
	}

	pvcsToCleanUp := []fs.FileInfo{}
	for _, pvcSubPath := range pvcSubPaths {
		log.Printf("Pvc sub-folder is %s ", pvcSubPath.Name())
		if !pvcSubPath.IsDir() {
			continue
		}

		isPresent := false
		for _, pipelinerun := range pipelineRuns.Items {
			log.Printf("pipelinerun %s and pvc subpath folder name is %s", "pvc-"+pipelinerun.ObjectMeta.Name, pvcSubPath.Name())
			if "pv-"+pipelinerun.ObjectMeta.Name == pvcSubPath.Name() {
				isPresent = true
				break
			}
		}
		if !isPresent {
			pvcsToCleanUp = append(pvcsToCleanUp, pvcSubPath)
		}
	}

	var wg sync.WaitGroup
	// Remove pvc subfolders in parallel.
	for _, pvc := range pvcsToCleanUp {
		wg.Add(1)
		go cleanUpSubpaths(pvc, &wg)
	}

	wg.Wait()
}

func watchNewPipelineRuns(pipelineRunApi v1beta1.PipelineRunInterface) {
	watch, err := pipelineRunApi.Watch(context.TODO(), metav1.ListOptions{})
	if err != nil {
		log.Fatal(err)
	}

	for event := range watch.ResultChan() {
		if event.Type != watchapi.Added {
			continue
		}
		_, ok := event.Object.(*pipelinev1.PipelineRun)
		if !ok {
			continue
		}

		// Stop appication, we shouldn't continue cleanup when new pipelinerun executed, because this
		// new pipelinerun will fail on the pvc without support parallel read/write operation from different pods
		os.Exit(0)
	}
}

func cleanUpSubpaths(pvc fs.FileInfo, wg *sync.WaitGroup) {
	defer wg.Done()

	path := filepath.Join(pkg.SOURCE_VOLUME_DIR, pvc.Name())
	info, err := os.Stat(path)
	if err != nil {
		log.Println(err)
		return
	}
	log.Printf("Data folder size is %d", info.Size())

	log.Printf("Remove pvc subpath: %s", path)
	if err := os.RemoveAll(path); err != nil {
		log.Println(err.Error())
		return
	}
	log.Printf("Cleanup %s completed", pvc.Name())
}
