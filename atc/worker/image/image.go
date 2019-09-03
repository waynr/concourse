package image

import (
	"context"
	"io"
	"net/url"
	"path"

	"code.cloudfoundry.org/lager"
	"github.com/concourse/baggageclaim"
	"github.com/concourse/concourse/atc"
	"github.com/concourse/concourse/atc/db"
	"github.com/concourse/concourse/atc/worker"
)

const OCIRootFSScheme = "oci"

type imageProvidedByPreviousStepOnSameWorker struct {
	artifactVolume worker.Volume
	imageSpec      worker.ImageSpec
	teamID         int
	volumeClient   worker.VolumeClient
}

func (i *imageProvidedByPreviousStepOnSameWorker) FetchForContainer(
	ctx context.Context,
	logger lager.Logger,
	container db.CreatingContainer,
) (worker.FetchedImage, error) {
	imageVolume, err := i.volumeClient.FindOrCreateCOWVolumeForContainer(
		logger,
		worker.VolumeSpec{
			Strategy:   i.artifactVolume.COWStrategy(),
			Privileged: i.imageSpec.Privileged,
		},
		container,
		i.artifactVolume,
		i.teamID,
		"/",
	)
	if err != nil {
		logger.Error("failed-to-create-image-artifact-cow-volume", err)
		return worker.FetchedImage{}, err
	}

	imageURL := url.URL{
		Scheme: OCIRootFSScheme,
		Path:   path.Join(imageVolume.Path(), "image.tar"),
	}

	return worker.FetchedImage{
		URL:        imageURL.String(),
		Privileged: i.imageSpec.Privileged,
	}, nil
}

type imageProvidedByPreviousStepOnDifferentWorker struct {
	imageSpec    worker.ImageSpec
	teamID       int
	volumeClient worker.VolumeClient
}

func (i *imageProvidedByPreviousStepOnDifferentWorker) FetchForContainer(
	ctx context.Context,
	logger lager.Logger,
	container db.CreatingContainer,
) (worker.FetchedImage, error) {
	imageVolume, err := i.volumeClient.FindOrCreateVolumeForContainer(
		logger,
		worker.VolumeSpec{
			Strategy:   baggageclaim.EmptyStrategy{},
			Privileged: i.imageSpec.Privileged,
		},
		container,
		i.teamID,
		"/",
	)
	if err != nil {
		logger.Error("failed-to-create-image-artifact-replicated-volume", err)
		return worker.FetchedImage{}, err
	}

	dest := artifactDestination{
		destination: imageVolume,
	}

	err = i.imageSpec.ImageArtifactSource.StreamTo(ctx, logger, &dest)
	if err != nil {
		logger.Error("failed-to-stream-image-artifact-source", err)
		return worker.FetchedImage{}, err
	}

	imageURL := url.URL{
		Scheme: OCIRootFSScheme,
		Path:   path.Join(imageVolume.Path(), "image.tar"),
	}

	return worker.FetchedImage{
		URL:        imageURL.String(),
		Privileged: i.imageSpec.Privileged,
	}, nil
}

type imageFromResource struct {
	privileged   bool
	teamID       int
	volumeClient worker.VolumeClient

	imageResourceFetcher ImageResourceFetcher
}

func (i *imageFromResource) FetchForContainer(
	ctx context.Context,
	logger lager.Logger,
	container db.CreatingContainer,
) (worker.FetchedImage, error) {
	imageParentVolume, version, err := i.imageResourceFetcher.Fetch(
		ctx,
		logger.Session("image"),
		container,
		i.privileged,
	)
	if err != nil {
		logger.Error("failed-to-fetch-image", err)
		return worker.FetchedImage{}, err
	}

	imageURL := url.URL{
		Scheme: OCIRootFSScheme,
		Path:   path.Join(imageParentVolume.Path(), "image.tar"),
	}

	return worker.FetchedImage{
		Version:    version,
		URL:        imageURL.String(),
		Privileged: i.privileged,
	}, nil
}

type imageFromBaseResourceType struct {
	worker           worker.Worker
	resourceTypeName string
	teamID           int
	volumeClient     worker.VolumeClient
}

func (i *imageFromBaseResourceType) FetchForContainer(
	ctx context.Context,
	logger lager.Logger,
	container db.CreatingContainer,
) (worker.FetchedImage, error) {
	for _, t := range i.worker.ResourceTypes() {
		if t.Type == i.resourceTypeName {
			rootFSURL := url.URL{
				Scheme: OCIRootFSScheme,
				Path:   t.Image,
			}

			return worker.FetchedImage{
				Version:    atc.Version{i.resourceTypeName: t.Version},
				URL:        rootFSURL.String(),
				Privileged: t.Privileged,
			}, nil
		}
	}

	return worker.FetchedImage{}, ErrUnsupportedResourceType
}

type imageFromRootfsURI struct {
	url string
}

func (i *imageFromRootfsURI) FetchForContainer(
	ctx context.Context,
	logger lager.Logger,
	container db.CreatingContainer,
) (worker.FetchedImage, error) {
	return worker.FetchedImage{
		URL: i.url,
	}, nil
}

type artifactDestination struct {
	destination worker.Volume
}

func (wad *artifactDestination) StreamIn(ctx context.Context, path string, tarStream io.Reader) error {
	return wad.destination.StreamIn(ctx, path, tarStream)
}
