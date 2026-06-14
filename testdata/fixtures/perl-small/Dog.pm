package Dog;

use strict;
use warnings;
use parent 'Animal';

sub speak {
    my $self = shift;
    return "Woof";
}

1;
