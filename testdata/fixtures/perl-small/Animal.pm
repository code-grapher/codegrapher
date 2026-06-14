package Animal;

use strict;
use warnings;

sub new {
    my ($class, %args) = @_;
    my $self = { name => $args{name} };
    return bless $self, $class;
}

sub speak {
    my $self = shift;
    return "Some generic sound";
}

1;
