package integration_test

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/paketo-buildpacks/occam"
	"github.com/sclevine/spec"

	. "github.com/onsi/gomega"
	. "github.com/paketo-buildpacks/occam/matchers"
)

func testLayerReuse(t *testing.T, context spec.G, it spec.S) {
	var (
		Expect     = NewWithT(t).Expect
		Eventually = NewWithT(t).Eventually

		docker occam.Docker
		pack   occam.Pack

		imageIDs     map[string]struct{}
		containerIDs map[string]struct{}

		name   string
		source string
	)

	it.Before(func() {
		var err error
		name, err = occam.RandomName()
		Expect(err).NotTo(HaveOccurred())

		docker = occam.NewDocker()
		pack = occam.NewPack()
		imageIDs = map[string]struct{}{}
		containerIDs = map[string]struct{}{}
	})

	it.After(func() {
		for id := range containerIDs {
			Expect(docker.Container.Remove.Execute(id)).To(Succeed())
		}

		for id := range imageIDs {
			Expect(docker.Image.Remove.Execute(id)).To(Succeed())
		}

		Expect(docker.Volume.Remove.Execute(occam.CacheVolumeNames(name))).To(Succeed())

		Expect(os.RemoveAll(source)).To(Succeed())
	})

	context("when an app is rebuilt and does not change", func() {
		it("reuses a layer from a previous build", func() {
			var (
				err         error
				logs        fmt.Stringer
				firstImage  occam.Image
				secondImage occam.Image

				firstContainer  occam.Container
				secondContainer occam.Container
			)

			build := pack.WithNoColor().Build.
				WithPullPolicy("never").
				WithBuildpacks(
					settings.Buildpacks.Cpython.Online,
					settings.Buildpacks.BuildPlan.Online,
				)

			source, err = occam.Source(filepath.Join("testdata", "default_app"))
			Expect(err).NotTo(HaveOccurred())

			firstImage, logs, err = build.
				Execute(name, source)
			Expect(err).NotTo(HaveOccurred())

			imageIDs[firstImage.ID] = struct{}{}

			Expect(firstImage.Buildpacks).To(HaveLen(2))
			Expect(firstImage.Buildpacks[0].Key).To(Equal(buildpackInfo.Buildpack.ID))
			Expect(firstImage.Buildpacks[0].Layers).To(HaveKey("cpython"))

			Expect(logs).To(ContainLines(
				MatchRegexp(fmt.Sprintf(`%s \d+\.\d+\.\d+`, buildpackInfo.Buildpack.Name)),
				"  Resolving CPython version",
				"    Candidate version sources (in priority order):",
				"      <unknown> -> \"\"",
				"",
				MatchRegexp(`    Selected CPython version \(using <unknown>\): 3\.\d+\.\d+`),
				"",
				"  Executing build process",
				MatchRegexp(`    Installing CPython 3\.\d+\.\d+`),
				MatchRegexp(`      Completed in \d+\.\d+`),
			))

			firstContainer, err = docker.Container.Run.
				WithCommand("python3 server.py").
				WithEnv(map[string]string{"PORT": "8080"}).
				WithPublish("8080").
				Execute(firstImage.ID)
			Expect(err).ToNot(HaveOccurred())

			containerIDs[firstContainer.ID] = struct{}{}

			Eventually(firstContainer).Should(BeAvailable())

			// Second pack build
			secondImage, logs, err = build.
				Execute(name, source)
			Expect(err).NotTo(HaveOccurred())

			imageIDs[secondImage.ID] = struct{}{}

			Expect(secondImage.Buildpacks).To(HaveLen(2))
			Expect(secondImage.Buildpacks[0].Key).To(Equal(buildpackInfo.Buildpack.ID))
			Expect(secondImage.Buildpacks[0].Layers).To(HaveKey("cpython"))

			Expect(logs).To(ContainLines(
				MatchRegexp(fmt.Sprintf(`%s \d+\.\d+\.\d+`, buildpackInfo.Buildpack.Name)),
				"  Resolving CPython version",
				"    Candidate version sources (in priority order):",
				"      <unknown> -> \"\"",
				"",
				MatchRegexp(`    Selected CPython version \(using <unknown>\): 3\.\d+\.\d+`),
				"",
				MatchRegexp(fmt.Sprintf("  Reusing cached layer /layers/%s/cpython", strings.ReplaceAll(buildpackInfo.Buildpack.ID, "/", "_"))),
			))

			secondContainer, err = docker.Container.Run.
				WithCommand("python3 server.py").
				WithEnv(map[string]string{"PORT": "8080"}).
				WithPublish("8080").
				Execute(secondImage.ID)
			Expect(err).ToNot(HaveOccurred())

			containerIDs[secondContainer.ID] = struct{}{}

			Eventually(secondContainer).Should(BeAvailable())
			Eventually(secondContainer).Should(Serve(ContainSubstring("hello world")).OnPort(8080))

			Expect(secondImage.Buildpacks[0].Layers["cpython"].Metadata["built_at"]).To(Equal(firstImage.Buildpacks[0].Layers["cpython"].Metadata["built_at"]))
		})
	})

	context("when an app is rebuilt and there is a change", func() {
		it("rebuilds the layer", func() {
			var (
				err         error
				logs        fmt.Stringer
				firstImage  occam.Image
				secondImage occam.Image

				firstContainer  occam.Container
				secondContainer occam.Container
			)

			source, err = occam.Source(filepath.Join("testdata", "buildpack_yml_app"))
			Expect(err).NotTo(HaveOccurred())
			// Overwrite the cpython version the buildpack.yml with a version from the buildpack.toml
			Expect(ioutil.WriteFile(filepath.Join(source, "buildpack.yml"), []byte(fmt.Sprintf("---\ncpython:\n  version: %s", buildpackInfo.Metadata.Dependencies[2].Version)), 0644)).To(Succeed())

			build := pack.WithNoColor().Build.
				WithPullPolicy("never").
				WithBuildpacks(
					settings.Buildpacks.Cpython.Online,
					settings.Buildpacks.BuildPlan.Online,
				)

			firstImage, logs, err = build.
				Execute(name, source)
			Expect(err).NotTo(HaveOccurred())

			imageIDs[firstImage.ID] = struct{}{}

			Expect(firstImage.Buildpacks).To(HaveLen(2))
			Expect(firstImage.Buildpacks[0].Key).To(Equal(buildpackInfo.Buildpack.ID))
			Expect(firstImage.Buildpacks[0].Layers).To(HaveKey("cpython"))

			Expect(logs).To(ContainLines(
				MatchRegexp(fmt.Sprintf(`%s \d+\.\d+\.\d+`, buildpackInfo.Buildpack.Name)),
				"  Resolving CPython version",
				"    Candidate version sources (in priority order):",
				MatchRegexp(`      buildpack.yml -> \"3\.\d+\.\d+\"`),
				"      <unknown>     -> \"\"",
				"",
				MatchRegexp(`    Selected CPython version \(using buildpack.yml\): 3\.\d+\.\d+`),
				"",
			))
			Expect(logs).To(ContainLines(
				"  Executing build process",
				MatchRegexp(`    Installing CPython 3\.\d+\.\d+`),
				MatchRegexp(`      Completed in \d+\.\d+`),
			))

			firstContainer, err = docker.Container.Run.
				WithCommand("python3 server.py").
				WithEnv(map[string]string{"PORT": "8080"}).
				WithPublish("8080").
				Execute(firstImage.ID)
			Expect(err).ToNot(HaveOccurred())

			containerIDs[firstContainer.ID] = struct{}{}

			Eventually(firstContainer).Should(BeAvailable())

			Expect(ioutil.WriteFile(filepath.Join(source, "buildpack.yml"), []byte(fmt.Sprintf("---\ncpython:\n  version: %s", buildpackInfo.Metadata.Dependencies[0].Version)), 0644)).To(Succeed())
			// Second pack build
			secondImage, logs, err = build.Execute(name, source)
			Expect(err).NotTo(HaveOccurred())

			imageIDs[secondImage.ID] = struct{}{}

			Expect(secondImage.Buildpacks).To(HaveLen(2))
			Expect(secondImage.Buildpacks[0].Key).To(Equal(buildpackInfo.Buildpack.ID))
			Expect(secondImage.Buildpacks[0].Layers).To(HaveKey("cpython"))

			Expect(logs).To(ContainLines(
				MatchRegexp(fmt.Sprintf(`%s \d+\.\d+\.\d+`, buildpackInfo.Buildpack.Name)),
				"  Resolving CPython version",
				"    Candidate version sources (in priority order):",
				MatchRegexp(`      buildpack.yml -> \"\d+\.\d+\.\d+\"`),
				"      <unknown>     -> \"\"",
				"",
				MatchRegexp(`    Selected CPython version \(using buildpack.yml\): 3\.\d+\.\d+`),
				"",
			))
			Expect(logs).To(ContainLines(
				"  Executing build process",
				MatchRegexp(`    Installing CPython 3\.\d+\.\d+`),
				MatchRegexp(`      Completed in \d+\.\d+`),
			))

			secondContainer, err = docker.Container.Run.
				WithCommand("python3 server.py").
				WithEnv(map[string]string{"PORT": "8080"}).
				WithPublish("8080").
				Execute(secondImage.ID)
			Expect(err).ToNot(HaveOccurred())

			containerIDs[secondContainer.ID] = struct{}{}

			Eventually(secondContainer).Should(BeAvailable())
			Eventually(secondContainer).Should(Serve(ContainSubstring("hello world")).OnPort(8080))

			Expect(secondImage.Buildpacks[0].Layers["cpython"].Metadata["built_at"]).NotTo(Equal(firstImage.Buildpacks[0].Layers["cpython"].Metadata["built_at"]))
		})
	})
}
